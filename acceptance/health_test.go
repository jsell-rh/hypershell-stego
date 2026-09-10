package acceptance

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestGatewayAPIHealthAcrossDatabaseDelayAndRestart(t *testing.T) {
	f := database(t)
	row, err := f.service.Create(context.Background(), principal("alice", "gateway:creator"), f.request("health-recovery"))
	if err != nil {
		t.Fatal(err)
	}
	proxy := newHealthDatabaseProxy(t, f.dsn)
	_, config := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	binary := buildApplication(t)
	stop, address := startApplication(t, binary, proxy.dsn, config, settings...)
	defer func() { stop() }()
	client := &http.Client{Timeout: time.Second}
	probe := func(path string, want int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			response, err := client.Get(address + path)
			if err == nil {
				body, readErr := io.ReadAll(io.LimitReader(response.Body, 1024))
				response.Body.Close()
				if response.StatusCode == want {
					expected := "ok\n"
					if want == 503 {
						expected = "unavailable\n"
					}
					if readErr != nil || string(body) != expected || response.Header.Get("Cache-Control") != "no-store" {
						t.Fatal("probe returned invalid or private data")
					}
					return
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s did not return %d", path, want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	probe("/livez", 200)
	probe("/readyz", 200)
	// Delay database traffic without closing the event subscription. This isolates
	// a dependency outage from the runtime's separate fatal-source contract.
	proxy.pause(true)
	defer proxy.pause(false)
	probe("/readyz", 503)
	probe("/livez", 200)
	proxy.pause(false)
	probe("/readyz", 200)
	root := address + "/api/hypershell/v1/gateways/" + row.ID
	if code, _ := requestJSON(t, "GET", root, token(t, key, "alice"), nil); code != 200 {
		t.Fatal("Gateway did not recover", code)
	}
	if code, _ := requestJSON(t, "GET", root, "", nil); code != 401 {
		t.Fatal("public probes bypassed Gateway authorization", code)
	}
	stop()
	stop, address = startApplication(t, binary, proxy.dsn, config, settings...)
	probe("/readyz", 200)
	if code, _ := requestJSON(t, "GET", address+"/api/hypershell/v1/gateways/"+row.ID, token(t, key, "alice"), nil); code != 200 {
		t.Fatal("Gateway was lost after restart", code)
	}
}

type healthDatabaseProxy struct {
	dsn  string
	mu   sync.Mutex
	gate chan struct{}
}

func (p *healthDatabaseProxy) pause(paused bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if paused {
		if p.gate == nil {
			p.gate = make(chan struct{})
		}
	} else if p.gate != nil {
		close(p.gate)
		p.gate = nil
	}
}
func (p *healthDatabaseProxy) wait(ctx context.Context) error {
	p.mu.Lock()
	gate := p.gate
	p.mu.Unlock()
	if gate == nil {
		return ctx.Err()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-gate:
		return nil
	}
}
func newHealthDatabaseProxy(t *testing.T, dsn string) *healthDatabaseProxy {
	t.Helper()
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	mapped := dsn + " host=" + host + " port=" + port
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		u.Host = listener.Addr().String()
		mapped = u.String()
	}
	p := &healthDatabaseProxy{dsn: mapped}
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		for {
			downstream, err := listener.Accept()
			if err != nil {
				return
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer downstream.Close()
				upstream, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))))
				if err != nil {
					return
				}
				defer upstream.Close()
				closeConnection := context.AfterFunc(ctx, func() { downstream.Close(); upstream.Close() })
				defer closeConnection()
				copyData := func(to, from net.Conn) {
					buffer := make([]byte, 32<<10)
					for {
						n, err := from.Read(buffer)
						if n > 0 {
							if p.wait(ctx) != nil {
								return
							}
							if _, writeErr := to.Write(buffer[:n]); writeErr != nil {
								return
							}
						}
						if err != nil {
							return
						}
					}
				}
				copied := make(chan struct{})
				go func() { defer close(copied); copyData(upstream, downstream); upstream.Close(); downstream.Close() }()
				copyData(downstream, upstream)
				upstream.Close()
				downstream.Close()
				<-copied
			}()
		}
	}()
	t.Cleanup(func() { cancel(); listener.Close(); <-accepted; workers.Wait() })
	return p
}
