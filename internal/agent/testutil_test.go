package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	openai "github.com/sashabaranov/go-openai"

	"github.com/chowyu12/aiclaw/internal/model"
	"github.com/chowyu12/aiclaw/internal/provider"
)

// ==================== Mock Store ====================

type mockStore struct {
	mu        sync.RWMutex
	nextIDVal atomic.Int64

	providers     map[int64]*model.Provider
	toolItems     map[int64]*model.Tool
	conversations map[int64]*model.Conversation
	convByUUID    map[string]*model.Conversation
	messages      map[int64][]model.Message
	execSteps     map[int64][]model.ExecutionStep

	getConvByUUIDErr error
}

func newMockStore() *mockStore {
	return &mockStore{
		providers:     make(map[int64]*model.Provider),
		toolItems:     make(map[int64]*model.Tool),
		conversations: make(map[int64]*model.Conversation),
		convByUUID:    make(map[string]*model.Conversation),
		messages:      make(map[int64][]model.Message),
		execSteps:     make(map[int64][]model.ExecutionStep),
	}
}

func (s *mockStore) nextID() int64 { return s.nextIDVal.Add(1) }
func (s *mockStore) Close() error  { return nil }

func (s *mockStore) CreateProvider(_ context.Context, p *model.Provider) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p.ID = s.nextID()
	s.providers[p.ID] = p
	return nil
}
func (s *mockStore) GetProvider(_ context.Context, id int64) (*model.Provider, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if p, ok := s.providers[id]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("provider %d not found", id)
}
func (s *mockStore) ListProviders(_ context.Context, _ model.ListQuery) ([]*model.Provider, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var list []*model.Provider
	for _, p := range s.providers {
		list = append(list, p)
	}
	return list, int64(len(list)), nil
}
func (s *mockStore) UpdateProvider(_ context.Context, _ int64, _ model.UpdateProviderReq) error {
	return nil
}
func (s *mockStore) DeleteProvider(_ context.Context, _ int64) error { return nil }

func (s *mockStore) CreateTool(_ context.Context, t *model.Tool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t.ID = s.nextID()
	s.toolItems[t.ID] = t
	return nil
}
func (s *mockStore) GetTool(_ context.Context, id int64) (*model.Tool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if t, ok := s.toolItems[id]; ok {
		return t, nil
	}
	return nil, fmt.Errorf("tool %d not found", id)
}
func (s *mockStore) ListTools(_ context.Context, _ model.ListQuery) ([]*model.Tool, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var list []*model.Tool
	for _, t := range s.toolItems {
		list = append(list, t)
	}
	return list, int64(len(list)), nil
}
func (s *mockStore) UpdateTool(_ context.Context, _ int64, _ model.UpdateToolReq) error { return nil }
func (s *mockStore) DeleteTool(_ context.Context, _ int64) error                        { return nil }

func (s *mockStore) CreateChannel(_ context.Context, c *model.Channel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.ID = s.nextID()
	if c.UUID == "" {
		c.UUID = fmt.Sprintf("ch-%d", c.ID)
	}
	return nil
}
func (s *mockStore) GetChannel(_ context.Context, id int64) (*model.Channel, error) {
	return nil, sql.ErrNoRows
}
func (s *mockStore) GetChannelByUUID(_ context.Context, uuid string) (*model.Channel, error) {
	return nil, sql.ErrNoRows
}
func (s *mockStore) ListChannels(_ context.Context, _ model.ListQuery) ([]*model.Channel, int64, error) {
	return nil, 0, nil
}
func (s *mockStore) UpdateChannel(_ context.Context, _ int64, _ model.UpdateChannelReq) error {
	return nil
}
func (s *mockStore) DeleteChannel(_ context.Context, _ int64) error { return nil }

func (s *mockStore) GetChannelThread(_ context.Context, _ int64, _ string) (*model.ChannelThread, error) {
	return nil, sql.ErrNoRows
}
func (s *mockStore) UpsertChannelThread(_ context.Context, _ int64, _, _ string) error { return nil }
func (s *mockStore) ListChannelThreads(_ context.Context, _ int64) ([]model.ChannelThread, error) {
	return nil, nil
}
func (s *mockStore) DeleteChannelThreadsByConversation(_ context.Context, _ int64, _ string) error {
	return nil
}

func (s *mockStore) CreateConversation(_ context.Context, c *model.Conversation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.ID = s.nextID()
	if c.UUID == "" {
		c.UUID = fmt.Sprintf("conv-%d", c.ID)
	}
	s.conversations[c.ID] = c
	s.convByUUID[c.UUID] = c
	return nil
}
func (s *mockStore) GetConversation(_ context.Context, id int64) (*model.Conversation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if c, ok := s.conversations[id]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("conversation %d not found", id)
}
func (s *mockStore) GetConversationByUUID(_ context.Context, uuid string) (*model.Conversation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.getConvByUUIDErr != nil {
		return nil, s.getConvByUUIDErr
	}
	if c, ok := s.convByUUID[uuid]; ok {
		return c, nil
	}
	return nil, sql.ErrNoRows
}
func (s *mockStore) ListConversations(_ context.Context, userID string, q model.ListQuery) ([]*model.Conversation, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var list []*model.Conversation
	for _, c := range s.conversations {
		if userID != "" && c.UserID != userID {
			continue
		}
		if q.Keyword != "" && !strings.Contains(c.Title, q.Keyword) {
			continue
		}
		list = append(list, c)
	}
	return list, int64(len(list)), nil
}
func (s *mockStore) UpdateConversationTitle(_ context.Context, id int64, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.conversations[id]
	if !ok {
		return sql.ErrNoRows
	}
	c.Title = title
	return nil
}
func (s *mockStore) DeleteConversation(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.conversations[id]
	if !ok {
		return nil
	}
	delete(s.conversations, id)
	delete(s.convByUUID, c.UUID)
	delete(s.messages, id)
	delete(s.execSteps, id)
	return nil
}
func (s *mockStore) CreateMessage(_ context.Context, m *model.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	m.ID = s.nextID()
	s.messages[m.ConversationID] = append(s.messages[m.ConversationID], *m)
	return nil
}
func (s *mockStore) CreateMessages(_ context.Context, msgs []*model.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range msgs {
		m.ID = s.nextID()
		s.messages[m.ConversationID] = append(s.messages[m.ConversationID], *m)
	}
	return nil
}
func (s *mockStore) ListMessages(_ context.Context, conversationID int64, limit int) ([]model.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	msgs := s.messages[conversationID]
	if limit > 0 && len(msgs) > limit {
		msgs = msgs[len(msgs)-limit:]
	}
	result := make([]model.Message, len(msgs))
	copy(result, msgs)
	return result, nil
}
func (s *mockStore) CreateExecutionStep(_ context.Context, step *model.ExecutionStep) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	step.ID = s.nextID()
	s.execSteps[step.ConversationID] = append(s.execSteps[step.ConversationID], *step)
	return nil
}
func (s *mockStore) UpdateStepsMessageID(_ context.Context, conversationID, messageID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	steps := s.execSteps[conversationID]
	for i := range steps {
		if steps[i].MessageID == 0 {
			steps[i].MessageID = messageID
		}
	}
	s.execSteps[conversationID] = steps
	return nil
}
func (s *mockStore) ListExecutionSteps(_ context.Context, messageID int64) ([]model.ExecutionStep, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []model.ExecutionStep
	for _, steps := range s.execSteps {
		for _, step := range steps {
			if step.MessageID == messageID {
				result = append(result, step)
			}
		}
	}
	return result, nil
}
func (s *mockStore) ListExecutionStepsByConversation(_ context.Context, convID int64) ([]model.ExecutionStep, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.execSteps[convID], nil
}

func (s *mockStore) CreateFile(_ context.Context, f *model.File) error {
	f.ID = s.nextID()
	return nil
}
func (s *mockStore) GetFileByUUID(_ context.Context, _ string) (*model.File, error) {
	return nil, fmt.Errorf("not found")
}
func (s *mockStore) ListFilesByConversation(_ context.Context, _ int64) ([]*model.File, error) {
	return nil, nil
}
func (s *mockStore) ListFilesByMessage(_ context.Context, _ int64) ([]*model.File, error) {
	return nil, nil
}
func (s *mockStore) UpdateFileMessageID(_ context.Context, _, _ int64) error  { return nil }
func (s *mockStore) LinkFileToMessage(_ context.Context, _, _, _ int64) error { return nil }
func (s *mockStore) DeleteFile(_ context.Context, _ int64) error              { return nil }

func (s *mockStore) ListMCPServers(_ context.Context) ([]model.MCPServer, error) {
	return nil, nil
}

func (s *mockStore) ReplaceMCPServers(_ context.Context, _ []model.MCPServer) error {
	return nil
}

// ==================== Mock LLM Provider ====================

type mockLLMProvider struct {
	mu            sync.Mutex
	responses     []openai.ChatCompletionResponse
	errors        []error
	callIdx       int
	calls         []openai.ChatCompletionRequest
	streamContent string
	streamErr     error
}

var _ provider.LLMProvider = (*mockLLMProvider)(nil)

func (m *mockLLMProvider) CreateChatCompletion(_ context.Context, req openai.ChatCompletionRequest) (openai.ChatCompletionResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, req)
	idx := m.callIdx
	m.callIdx++
	if idx < len(m.errors) && m.errors[idx] != nil {
		return openai.ChatCompletionResponse{}, m.errors[idx]
	}
	if idx < len(m.responses) {
		return m.responses[idx], nil
	}
	return openai.ChatCompletionResponse{Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Content: ""}}}}, nil
}

func (m *mockLLMProvider) CreateChatCompletionStream(_ context.Context, req openai.ChatCompletionRequest) (provider.ChatStream, error) {
	m.mu.Lock()
	m.calls = append(m.calls, req)
	idx := m.callIdx
	m.callIdx++
	streamErr := m.streamErr
	content := m.streamContent
	var resp openai.ChatCompletionResponse
	hasResp := idx < len(m.responses)
	if hasResp {
		resp = m.responses[idx]
	}
	m.mu.Unlock()

	if streamErr != nil {
		return nil, streamErr
	}

	if hasResp {
		return respToStream(resp), nil
	}

	const chunkSize = 10
	var chunks []openai.ChatCompletionStreamResponse
	for i := 0; i < len(content); i += chunkSize {
		end := min(i+chunkSize, len(content))
		chunks = append(chunks, openai.ChatCompletionStreamResponse{
			Choices: []openai.ChatCompletionStreamChoice{{
				Delta: openai.ChatCompletionStreamChoiceDelta{
					Content: content[i:end],
				},
			}},
		})
	}
	return &mockChatStream{chunks: chunks}, nil
}

func respToStream(resp openai.ChatCompletionResponse) provider.ChatStream {
	var chunks []openai.ChatCompletionStreamResponse
	if len(resp.Choices) == 0 {
		return &mockChatStream{chunks: chunks}
	}
	choice := resp.Choices[0]

	for i, tc := range choice.Message.ToolCalls {
		idx := i
		chunks = append(chunks, openai.ChatCompletionStreamResponse{
			Choices: []openai.ChatCompletionStreamChoice{{
				Delta: openai.ChatCompletionStreamChoiceDelta{
					ToolCalls: []openai.ToolCall{{
						Index:    &idx,
						ID:       tc.ID,
						Type:     tc.Type,
						Function: tc.Function,
					}},
				},
			}},
		})
	}

	if choice.Message.Content != "" {
		content := choice.Message.Content
		const chunkSize = 10
		for i := 0; i < len(content); i += chunkSize {
			end := min(i+chunkSize, len(content))
			chunks = append(chunks, openai.ChatCompletionStreamResponse{
				Choices: []openai.ChatCompletionStreamChoice{{
					Delta: openai.ChatCompletionStreamChoiceDelta{
						Content: content[i:end],
					},
				}},
			})
		}
	}

	finishReason := openai.FinishReasonStop
	if len(choice.Message.ToolCalls) > 0 {
		finishReason = openai.FinishReasonToolCalls
	}
	chunks = append(chunks, openai.ChatCompletionStreamResponse{
		Choices: []openai.ChatCompletionStreamChoice{{
			FinishReason: finishReason,
		}},
	})

	return &mockChatStream{chunks: chunks}
}

func (m *mockLLMProvider) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

type mockChatStream struct {
	chunks []openai.ChatCompletionStreamResponse
	idx    int
}

func (s *mockChatStream) Recv() (openai.ChatCompletionStreamResponse, error) {
	if s.idx >= len(s.chunks) {
		return openai.ChatCompletionStreamResponse{}, io.EOF
	}
	chunk := s.chunks[s.idx]
	s.idx++
	return chunk, nil
}

func (s *mockChatStream) Close() error { return nil }

// ==================== Test Helpers ====================

func testJSON(v any) model.JSON {
	data, _ := json.Marshal(v)
	return model.JSON(data)
}

func seedAgent(t *testing.T, s *mockStore) (*model.Agent, *model.Provider) {
	t.Helper()
	t.Cleanup(ClearTestAgent)
	ctx := t.Context()
	p := &model.Provider{Name: "test-prov", Type: model.ProviderOpenAI, APIKey: "k", Enabled: true}
	if err := s.CreateProvider(ctx, p); err != nil {
		t.Fatal(err)
	}
	a := &model.Agent{
		UUID: "test-agent", Name: "TestBot", ProviderID: p.ID,
		ModelName: "gpt-test", Temperature: 0.5, MaxTokens: 512,
		SystemPrompt: "你是一个测试助手",
	}
	SetTestAgent(a)
	return a, p
}

func seedToolForAgent(t *testing.T, s *mockStore, _ int64, name, desc string) *model.Tool {
	t.Helper()
	ctx := t.Context()
	tool := &model.Tool{
		Name: name, Description: desc, HandlerType: model.HandlerBuiltin, Enabled: true,
		FunctionDef: testJSON(map[string]any{
			"name": name, "description": desc,
			"parameters": map[string]any{"type": "object", "properties": map[string]any{}},
		}),
	}
	if err := s.CreateTool(ctx, tool); err != nil {
		t.Fatal(err)
	}
	ag, err := LoadAgent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ag.ToolIDs = append(ag.ToolIDs, tool.ID)
	SetTestAgent(ag)
	return tool
}

func newTestExecutor(s *mockStore, registry *ToolRegistry, mockLLM *mockLLMProvider) *Executor {
	return NewExecutor(s, registry, WithProviderFactory(
		func(_ *model.Provider, _ string) (provider.LLMProvider, error) {
			return mockLLM, nil
		},
	))
}

func textResp(content string) openai.ChatCompletionResponse {
	return openai.ChatCompletionResponse{
		Choices: []openai.ChatCompletionChoice{{Message: openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: content}}},
	}
}

func toolCallResp(toolName, args string) openai.ChatCompletionResponse {
	return openai.ChatCompletionResponse{
		Choices: []openai.ChatCompletionChoice{{
			Message: openai.ChatCompletionMessage{
				Role: openai.ChatMessageRoleAssistant,
				ToolCalls: []openai.ToolCall{{
					ID: "tc_" + toolName, Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{Name: toolName, Arguments: args},
				}},
			},
		}},
	}
}
