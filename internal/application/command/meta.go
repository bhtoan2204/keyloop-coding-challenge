package command

// Meta identifies who issued a command and how to correlate it. It is stored in the
// metadata of every event the command produces.
type Meta struct {
	ActorID       string
	CorrelationID string
	TraceParent   string
}

func (m Meta) toMap() map[string]interface{} {
	md := map[string]interface{}{"actor_id": m.ActorID, "correlation_id": m.CorrelationID}
	if m.TraceParent != "" {
		md["traceparent"] = m.TraceParent
	}
	return md
}
