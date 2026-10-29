package ledger

// WithoutSecrets copies JSON-like data while omitting credential-reference containers.
func WithoutSecrets(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			if key != "secrets" {
				out[key] = WithoutSecrets(child)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = WithoutSecrets(child)
		}
		return out
	default:
		return value
	}
}
