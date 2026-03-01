package agent

import (
	"fmt"
	"strings"
)

// --- Core message types ---

// Message represents a chat message in the conversation.
type Message struct {
	Role       string     `json:"role"`                  // "system", "user", "assistant", "tool"
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"` // set when Role == "tool"
	Name       string     `json:"name,omitempty"`         // tool name when Role == "tool"
}

// ToolCall represents an LLM's request to invoke a tool.
type ToolCall struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Args     map[string]any `json:"args"`
	RawArgs  string         `json:"-"` // raw JSON string from LLM
}

// ToolResult holds the output of a tool execution.
type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Output     string `json:"output"`
	Error      string `json:"error,omitempty"`
}

// --- Role constants ---

const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// ValidRole returns true if r is a known message role.
func ValidRole(r string) bool {
	switch r {
	case RoleSystem, RoleUser, RoleAssistant, RoleTool:
		return true
	}
	return false
}

// UserInputRole returns true if the role is allowed in user-submitted messages.
func UserInputRole(r string) bool {
	return r == RoleUser || r == RoleSystem
}

// --- Constructors (LangChain-style typed message creation) ---

// Human creates a user message.
//
//	Human("hello") → Message{Role:"user", Content:"hello"}
func Human(content string) Message {
	return Message{Role: RoleUser, Content: content}
}

// System creates a system message.
//
//	System("You are a helpful assistant.") → Message{Role:"system", Content:"..."}
func System(content string) Message {
	return Message{Role: RoleSystem, Content: content}
}

// AI creates an assistant message with optional tool calls.
//
//	AI("Sure, I can help.")                      → plain response
//	AI("", tc1, tc2)                             → tool-calling response
func AI(content string, toolCalls ...ToolCall) Message {
	return Message{Role: RoleAssistant, Content: content, ToolCalls: toolCalls}
}

// ToolMsg creates a tool result message.
//
//	ToolMsg("call_123", "calculate", "42")
func ToolMsg(toolCallID, name, output string) Message {
	return Message{Role: RoleTool, Content: output, ToolCallID: toolCallID, Name: name}
}

// --- Messages chain type ---

// Messages is an ordered list of messages with builder methods for chain construction.
//
// Usage:
//
//	chain := NewMessages().
//	    System("You are helpful.").
//	    Human("What is 2+2?")
//
//	// After LLM response:
//	chain = chain.AI("Let me calculate.", calcToolCall).
//	    Tool("call_1", "calculate", "4").
//	    AI("2+2 = 4")
type Messages []Message

// NewMessages creates an empty message chain.
func NewMessages(msgs ...Message) Messages {
	return Messages(msgs)
}

// System appends a system message and returns the chain.
func (m Messages) System(content string) Messages {
	return append(m, System(content))
}

// Human appends a user message and returns the chain.
func (m Messages) Human(content string) Messages {
	return append(m, Human(content))
}

// AI appends an assistant message and returns the chain.
func (m Messages) AI(content string, toolCalls ...ToolCall) Messages {
	return append(m, AI(content, toolCalls...))
}

// Tool appends a tool result message and returns the chain.
func (m Messages) Tool(toolCallID, name, output string) Messages {
	return append(m, ToolMsg(toolCallID, name, output))
}

// Add appends one or more messages and returns the chain.
func (m Messages) Add(msgs ...Message) Messages {
	return append(m, msgs...)
}

// Concat merges another chain onto this one.
func (m Messages) Concat(other Messages) Messages {
	return append(m, other...)
}

// Last returns the last message, or a zero Message if empty.
func (m Messages) Last() Message {
	if len(m) == 0 {
		return Message{}
	}
	return m[len(m)-1]
}

// LastContent returns the content of the last message.
func (m Messages) LastContent() string {
	return m.Last().Content
}

// Len returns the number of messages.
func (m Messages) Len() int {
	return len(m)
}

// Slice returns the underlying []Message.
func (m Messages) Slice() []Message {
	return []Message(m)
}

// --- Filtering ---

// ByRole returns messages with the given role.
func (m Messages) ByRole(role string) Messages {
	var out Messages
	for _, msg := range m {
		if msg.Role == role {
			out = append(out, msg)
		}
	}
	return out
}

// UserMessages returns only user messages.
func (m Messages) UserMessages() Messages { return m.ByRole(RoleUser) }

// AssistantMessages returns only assistant messages.
func (m Messages) AssistantMessages() Messages { return m.ByRole(RoleAssistant) }

// ToolMessages returns only tool result messages.
func (m Messages) ToolMessages() Messages { return m.ByRole(RoleTool) }

// SystemMessages returns only system messages.
func (m Messages) SystemMessages() Messages { return m.ByRole(RoleSystem) }

// --- Validation ---

// Validate checks that the message chain is well-formed:
//   - All roles are valid
//   - Tool messages have ToolCallID and Name set
//   - Assistant messages with ToolCalls have non-empty call IDs
//   - No empty content (except assistant messages with tool calls)
func (m Messages) Validate() error {
	for i, msg := range m {
		if !ValidRole(msg.Role) {
			return fmt.Errorf("message[%d]: unknown role %q", i, msg.Role)
		}

		switch msg.Role {
		case RoleTool:
			if msg.ToolCallID == "" {
				return fmt.Errorf("message[%d]: tool message missing tool_call_id", i)
			}
			if msg.Name == "" {
				return fmt.Errorf("message[%d]: tool message missing name", i)
			}

		case RoleAssistant:
			// Assistant messages can have empty content if they have tool calls
			if msg.Content == "" && len(msg.ToolCalls) == 0 {
				return fmt.Errorf("message[%d]: assistant message has no content and no tool calls", i)
			}
			for j, tc := range msg.ToolCalls {
				if tc.ID == "" {
					return fmt.Errorf("message[%d].tool_calls[%d]: missing ID", i, j)
				}
				if tc.Name == "" {
					return fmt.Errorf("message[%d].tool_calls[%d]: missing name", i, j)
				}
			}

		case RoleUser, RoleSystem:
			if msg.Content == "" {
				return fmt.Errorf("message[%d]: %s message has empty content", i, msg.Role)
			}
		}
	}
	return nil
}

// ValidateUserInput checks that messages are valid for user submission
// (only user and system roles allowed).
func (m Messages) ValidateUserInput() error {
	if len(m) == 0 {
		return fmt.Errorf("messages must not be empty")
	}
	for i, msg := range m {
		if !UserInputRole(msg.Role) {
			return fmt.Errorf("message[%d]: role %q not allowed (must be \"user\" or \"system\")", i, msg.Role)
		}
		if msg.Content == "" {
			return fmt.Errorf("message[%d]: content must not be empty", i)
		}
	}
	return nil
}

// --- Display ---

// PrettyPrint returns a human-readable representation of the message chain.
func (m Messages) PrettyPrint() string {
	var sb strings.Builder
	for _, msg := range m {
		sb.WriteString(prettyMessage(msg))
		sb.WriteString("\n")
	}
	return sb.String()
}

// String implements fmt.Stringer.
func (m Messages) String() string {
	return m.PrettyPrint()
}

func prettyMessage(msg Message) string {
	var sb strings.Builder
	label := roleLabel(msg.Role)

	if msg.Role == RoleTool {
		sb.WriteString(fmt.Sprintf("[%s: %s (call_id=%s)]\n", label, msg.Name, msg.ToolCallID))
	} else {
		sb.WriteString(fmt.Sprintf("[%s]\n", label))
	}

	if msg.Content != "" {
		sb.WriteString(msg.Content)
		sb.WriteString("\n")
	}

	for _, tc := range msg.ToolCalls {
		sb.WriteString(fmt.Sprintf("  → tool_call: %s(id=%s, args=%v)\n", tc.Name, tc.ID, tc.Args))
	}

	return sb.String()
}

func roleLabel(role string) string {
	switch role {
	case RoleSystem:
		return "System"
	case RoleUser:
		return "Human"
	case RoleAssistant:
		return "AI"
	case RoleTool:
		return "Tool"
	default:
		return role
	}
}

// --- Token estimation ---

// EstimateTokens returns a rough token count (len/4 heuristic).
func (m Messages) EstimateTokens() int {
	total := 0
	for _, msg := range m {
		total += len(msg.Content) / 4
		for _, tc := range msg.ToolCalls {
			total += len(tc.RawArgs) / 4
		}
	}
	return total
}
