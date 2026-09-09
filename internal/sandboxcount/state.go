package sandboxcount

import (
	"errors"
	"regexp"
	"sync"

	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

const SandboxLabel = "agents.x-k8s.io/sandbox-name-hash"

var namespacePattern = regexp.MustCompile(`^openshell-(?:sandbox-)?([0-9a-f]{16})$`)

type pod struct {
	namespace, version, physical string
	active                       bool
}
type observation struct {
	mu     sync.Mutex
	ready  bool
	pods   map[string]pod
	counts map[string]int32
	dirty  map[string]bool
	wake   chan struct{}
}

func newObservation() *observation {
	return &observation{pods: map[string]pod{}, counts: map[string]int32{}, dirty: map[string]bool{}, wake: make(chan struct{}, 1)}
}
func classify(o kube.Object) (string, pod, error) {
	uid := kube.String(o, "metadata", "uid")
	ns := kube.String(o, "metadata", "namespace")
	version := kube.String(o, "metadata", "resourceVersion")
	if uid == "" || ns == "" || version == "" {
		return "", pod{}, errors.New("sandbox observation has no identity")
	}
	match := namespacePattern.FindStringSubmatch(ns)
	canonical := ""
	if len(match) == 2 {
		canonical = "openshell-" + match[1]
	}
	labels, _ := kube.Nested(o, "metadata", "labels").(map[string]any)
	_, sandbox := labels[SandboxLabel]
	phase := kube.String(o, "status", "phase")
	return uid, pod{canonical, version, ns, canonical != "" && sandbox && (phase == "Pending" || phase == "Running")}, nil
}
func (o *observation) notify() {
	select {
	case o.wake <- struct{}{}:
	default:
	}
}
func (o *observation) consume(change kube.Change) (err error) {
	o.mu.Lock()
	defer func() {
		if err != nil {
			o.ready = false
		}
		o.mu.Unlock()
	}()
	if change.Type == "RESET" {
		o.ready = false
		return nil
	}
	if change.Type == "REPLACE" {
		next := newObservation()
		for _, object := range change.Objects {
			if err := next.update("ADDED", object); err != nil {
				return err
			}
		}
		for ns := range o.counts {
			next.dirty[ns] = true
		}
		for ns := range o.dirty {
			next.dirty[ns] = true
		}
		if len(next.dirty) > kube.MaxObservedObjects {
			return errors.New("sandbox count queue exceeds its limit")
		}
		o.pods, o.counts, o.dirty = next.pods, next.counts, next.dirty
		o.ready = true
		o.notify()
		return nil
	}
	if !o.ready {
		return errors.New("sandbox event arrived before the baseline")
	}
	if err := o.update(change.Type, change.Object); err != nil {
		return err
	}
	o.notify()
	return nil
}
func (o *observation) update(kind string, object kube.Object) error {
	uid, next, err := classify(object)
	if err != nil {
		return err
	}
	old, exists := o.pods[uid]
	if exists && old.physical != next.physical {
		return errors.New("sandbox UID changed namespace")
	}
	switch kind {
	case "DELETED":
		if !exists {
			return nil
		}
		delete(o.pods, uid)
		next.active = false
	case "ADDED", "MODIFIED":
		if exists && old.version == next.version {
			return nil
		}
		if !exists && len(o.pods) >= kube.MaxObservedObjects {
			return errors.New("sandbox cache exceeds its limit")
		}
		o.pods[uid] = next
	default:
		return errors.New("unknown sandbox event")
	}
	if old.active != next.active {
		ns := next.namespace
		if old.active {
			ns = old.namespace
			o.counts[ns]--
		} else {
			o.counts[ns]++
		}
		if o.counts[ns] == 0 {
			delete(o.counts, ns)
		}
		o.dirty[ns] = true
		if len(o.dirty) > kube.MaxObservedObjects {
			return errors.New("sandbox count queue exceeds its limit")
		}
	}
	return nil
}
