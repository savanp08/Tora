package ai

import "strings"

// ContextLimiter is an optional interface for providers that want to advertise
// their maximum input token budget. The router uses this to compact prompts
// before dispatch so the provider never receives a request that would exceed
// its hard context limit.
type ContextLimiter interface {
	MaxInputTokens() int
}

const (
	// defaultMaxInputTokens is the global upper bound used when a provider
	// does not implement ContextLimiter. 150 K is safely below every
	// provider's hard limit while leaving headroom for the completion.
	defaultMaxInputTokens = 150_000

	// charsPerToken is the approximation used to convert character counts to
	// token estimates. GPT-family tokenisers average ~4 chars/token for
	// English prose; we use the same heuristic across all providers.
	charsPerToken = 4
)

// estimateTokens returns a rough token count for text.
func estimateTokens(text string) int {
	n := len(text)
	if n == 0 {
		return 0
	}
	return (n + charsPerToken - 1) / charsPerToken
}

// compactForProvider compacts prompt for the specific provider's token limit.
// If the provider does not implement ContextLimiter, defaultMaxInputTokens is
// used as the ceiling.
func compactForProvider(prompt string, provider Summarizer) string {
	maxTokens := defaultMaxInputTokens
	if limiter, ok := provider.(ContextLimiter); ok {
		if limit := limiter.MaxInputTokens(); limit > 0 {
			maxTokens = limit
		}
	}
	return CompactPrompt(prompt, maxTokens)
}

// CompactPrompt trims the prompt to fit within maxTokens while preserving
// accuracy as much as possible.
//
// Priority order (most protected → first trimmed):
//  1. System instruction, workspace context, conversation summary — never
//     trimmed unless absolutely unavoidable.
//  2. Recent chat messages — primary trim target because this section grows
//     unboundedly; oldest messages are dropped first.
//  3. Workspace context section — tail-truncated only when (1) and (2)
//     cannot make enough room.
//
// The user's new message at the end of the prompt is always preserved.
func CompactPrompt(prompt string, maxTokens int) string {
	if maxTokens <= 0 {
		maxTokens = defaultMaxInputTokens
	}
	if estimateTokens(prompt) <= maxTokens {
		return prompt
	}

	const chatStart = "--- RECENT CHAT MESSAGES (private, only visible to you and this user) ---\n"
	const chatEnd = "\n--- END CHAT ---"

	startIdx := strings.Index(prompt, chatStart)
	endIdx := strings.Index(prompt, chatEnd)

	if startIdx < 0 || endIdx < 0 || endIdx <= startIdx {
		// No parseable chat block — fall back to tail-truncation of the full
		// prompt, keeping the last portion (which contains the user message).
		return tailTruncate(prompt, maxTokens)
	}

	// Split into: before-chat | chat-lines | after-chat
	before := prompt[:startIdx]
	chatLines := prompt[startIdx+len(chatStart) : endIdx]
	after := prompt[endIdx+len(chatEnd):]

	// Calculate how many chars the chat block can use after fixed parts
	// consume their share of the budget.
	fixedChars := len(before) + len(chatStart) + len(chatEnd) + len(after)
	budgetChars := maxTokens*charsPerToken - fixedChars

	if budgetChars <= 0 {
		// Fixed parts already exceed the limit — drop the chat section
		// entirely and tail-truncate the workspace block inside `before`.
		afterTokens := estimateTokens(after)
		beforeMaxTokens := maxTokens - afterTokens
		if beforeMaxTokens <= 0 {
			beforeMaxTokens = maxTokens / 2
		}
		trimmedBefore := tailTruncate(before, beforeMaxTokens)
		return trimmedBefore + after
	}

	trimmedChat := trimChatLinesToBudget(chatLines, budgetChars)

	var sb strings.Builder
	sb.WriteString(before)
	sb.WriteString(chatStart)
	sb.WriteString(trimmedChat)
	sb.WriteString(chatEnd)
	sb.WriteString(after)
	return sb.String()
}

// trimChatLinesToBudget keeps the most-recent lines from chatLines that fit
// within budgetChars, dropping older lines from the top.
func trimChatLinesToBudget(chatLines string, budgetChars int) string {
	if len(chatLines) <= budgetChars {
		return chatLines
	}
	lines := strings.Split(chatLines, "\n")
	for len(lines) > 1 && len(strings.Join(lines, "\n")) > budgetChars {
		lines = lines[1:]
	}
	result := strings.Join(lines, "\n")
	if len(result) > budgetChars {
		// Single line still too long — hard-truncate from the left so the
		// end of the line (more recent text) is preserved.
		result = result[len(result)-budgetChars:]
	}
	return result
}

// tailTruncate keeps the last (maxTokens * charsPerToken) chars of text and
// prepends a short notice so the model knows context was trimmed.
func tailTruncate(text string, maxTokens int) string {
	const notice = "[Context trimmed to fit token limit]\n"
	budget := maxTokens*charsPerToken - len(notice)
	if budget <= 0 || len(text) <= len(notice)+budget {
		return text
	}
	return notice + text[len(text)-budget:]
}
