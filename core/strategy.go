package core

type Strategy interface {
	Select(providers []Provider, req *ChatRequest, metrics *MetricsSnapshot) []Provider
}
