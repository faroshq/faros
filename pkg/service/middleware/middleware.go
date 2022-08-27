package middleware

type contextKey int

const (
	ContextKeyLog contextKey = iota
	ContextKeyOriginalPath
	ContextKeyBody
	ContextKeyCorrelationData

	ContextKeyClusterAccessSession
	ContextKeyCluster
)
