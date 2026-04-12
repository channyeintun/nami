package permissions

// CloneContext returns a shallow copy of the permission context with copied rule slices.
func CloneContext(src *Context) *Context {
	if src == nil {
		return NewContext()
	}
	cloned := &Context{
		Mode:            src.Mode,
		SessionAllowAll: src.SessionAllowAll,
	}
	cloned.AlwaysAllowRules = append([]Rule(nil), src.AlwaysAllowRules...)
	cloned.AlwaysDenyRules = append([]Rule(nil), src.AlwaysDenyRules...)
	cloned.AlwaysAskRules = append([]Rule(nil), src.AlwaysAskRules...)
	return cloned
}
