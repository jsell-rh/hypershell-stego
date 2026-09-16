package gateways

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/segmentio/ksuid"
)

func TestSQLComponentRejectsUnknownValues(t *testing.T) {
	service := new(Service)
	id, cluster := ksuid.New().String(), ksuid.New().String()
	for _, component := range []SQLComponent{-1, 2, 256} {
		if _, err := service.SQLStateBinding(context.Background(), Principal{}, id, cluster, component); !errors.Is(err, ErrInvalid) {
			t.Fatal("unknown component reached storage", err)
		}
		if _, err := service.BindSQLState(context.Background(), Principal{}, id, cluster, strings.Repeat("a", 64), 1, component); !errors.Is(err, ErrInvalid) {
			t.Fatal("unknown component reached registration", err)
		}
		if _, err := service.CloseSQLState(context.Background(), Principal{}, id, cluster, component); !errors.Is(err, ErrInvalid) {
			t.Fatal("unknown component reached closure", err)
		}
	}
	if sqlStateScope(cluster, SQLComponentGateway) != "sql-state:"+cluster {
		t.Fatal("existing Gateway state scope changed")
	}
	if sqlStateScope(cluster, SQLComponentConsole) == sqlStateScope(cluster, SQLComponentGateway) {
		t.Fatal("component state scopes overlap")
	}
}
