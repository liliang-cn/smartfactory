package sfconv

import (
	"github.com/gopcua/opcua/server"
	"github.com/gopcua/opcua/ua"
)

// LockedNamespace is gopcua's MapNamespace with reads taken under its lock: upstream
// Attribute reads the map unlocked, so a client read racing SetValue kills the process
// ("concurrent map read and map write").
//
// The inner namespace is created against a throwaway server so that the real server only ever
// sees (and dispatches to) this wrapper; every path that would notify the inner namespace's
// server is overridden here to notify the real one.
type LockedNamespace struct {
	*server.MapNamespace
	srv *server.Server
}

func NewLockedNamespace(srv *server.Server, name string) *LockedNamespace {
	ns := &LockedNamespace{MapNamespace: server.NewMapNamespace(server.New(), name), srv: srv}
	// upstream's channel is unbuffered and written with a non-blocking send: buffer it so a
	// client write is not lost while the application is busy
	ns.ExternalNotification = make(chan string, 256)
	srv.AddNamespace(ns)
	return ns
}

func (n *LockedNamespace) Attribute(id *ua.NodeID, a ua.AttributeID) *ua.DataValue {
	n.Mu.RLock()
	defer n.Mu.RUnlock()
	return n.MapNamespace.Attribute(id, a)
}

func (n *LockedNamespace) ChangeNotification(key string) {
	n.srv.ChangeNotification(ua.NewStringNodeID(n.ID(), key))
}

func (n *LockedNamespace) SetValue(key string, value any) {
	n.Mu.Lock()
	n.Data[key] = value
	n.Mu.Unlock()
	n.ChangeNotification(key)
}

// SetAttribute handles client writes: stores the value and tells the application which key changed.
func (n *LockedNamespace) SetAttribute(id *ua.NodeID, attr ua.AttributeID, val *ua.DataValue) ua.StatusCode {
	key := id.StringID()
	if attr == ua.AttributeIDValue && val != nil && val.Value != nil {
		n.Mu.Lock()
		n.Data[key] = val.Value.Value()
		n.Mu.Unlock()
	}
	n.srv.ChangeNotification(id)
	select {
	case n.ExternalNotification <- key:
	default:
	}
	return ua.StatusOK
}
