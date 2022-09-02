package middleware

type contextKey int

const (
	ContextKeyLog contextKey = iota
	ContextKeyCorrelationData
	ContextKeyClusterAccessSession
	ContextKeyUserAccessSession
	ContextKeyCluster
)
