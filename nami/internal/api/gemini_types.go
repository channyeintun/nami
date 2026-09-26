package api

type geminiGenerateContentRequest struct {
	Contents          []geminiContent        `json:"contents"`
	SystemInstruction *geminiContent         `json:"systemInstruction,omitempty"`
	Tools             []geminiTool           `json:"tools,omitempty"`
	GenerationConfig  geminiGenerationConfig `json:"generationConfig"`
}

type geminiGenerationConfig struct {
	Temperature     *float64 `json:"temperature,omitempty"`
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
	ThoughtSignature string                  `json:"thoughtSignature,omitempty"`
	InlineData       *geminiInlineData       `json:"inlineData,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiFunctionCall struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	Args any    `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response,omitempty"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations,omitempty"`
}

type geminiFunctionDeclaration struct {
	Name                 string `json:"name"`
	Description          string `json:"description,omitempty"`
	ParametersJsonSchema any    `json:"parametersJsonSchema,omitempty"`
}

type geminiGenerateContentResponse struct {
	Candidates     []geminiCandidate     `json:"candidates,omitempty"`
	PromptFeedback *geminiPromptFeedback `json:"promptFeedback,omitempty"`
	UsageMetadata  *geminiUsageMetadata  `json:"usageMetadata,omitempty"`
	Error          *geminiErrorBody      `json:"error,omitempty"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason,omitempty"`
}

type geminiPromptFeedback struct {
	BlockReason string `json:"blockReason,omitempty"`
}

type geminiUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount,omitempty"`
	CachedContentTokenCount int `json:"cachedContentTokenCount,omitempty"`
	CandidatesTokenCount    int `json:"candidatesTokenCount,omitempty"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount,omitempty"`
	TotalTokenCount         int `json:"totalTokenCount,omitempty"`
}

func (u *geminiUsageMetadata) merge(other *geminiUsageMetadata) {
	if other == nil {
		return
	}
	if other.PromptTokenCount > 0 {
		u.PromptTokenCount = other.PromptTokenCount
	}
	if other.CachedContentTokenCount > 0 {
		u.CachedContentTokenCount = other.CachedContentTokenCount
	}
	if other.CandidatesTokenCount > 0 {
		u.CandidatesTokenCount = other.CandidatesTokenCount
	}
	if other.ThoughtsTokenCount > 0 {
		u.ThoughtsTokenCount = other.ThoughtsTokenCount
	}
	if other.TotalTokenCount > 0 {
		u.TotalTokenCount = other.TotalTokenCount
	}
}

// toUsage counts thinking, which Gemini reports apart from the candidates but
// bills as output, as output, and reports the cached prefix, which the prompt
// count includes, apart from the rest of the prompt.
func (u geminiUsageMetadata) toUsage() *Usage {
	return &Usage{
		InputTokens:     max(u.PromptTokenCount-u.CachedContentTokenCount, 0),
		OutputTokens:    u.CandidatesTokenCount + u.ThoughtsTokenCount,
		CacheReadTokens: u.CachedContentTokenCount,
	}
}

type geminiErrorEnvelope struct {
	Error *geminiErrorBody `json:"error,omitempty"`
}

type geminiErrorBody struct {
	Status  string `json:"status,omitempty"`
	Message string `json:"message,omitempty"`
}

type geminiStreamState struct {
	usage      geminiUsageMetadata
	stopReason string
	sentStop   bool
}
