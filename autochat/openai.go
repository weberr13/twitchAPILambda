package autochat

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// OpenAI client
type OpenAI struct {
	client          openai.Client
	model           string
	nextRequestAt   map[string]time.Time
	globalRateLimit time.Time
	sync.RWMutex
}

// NewOpenAI client with given token
func NewOpenAI(token string) *OpenAI {
	cl := &OpenAI{
		client: *openai.NewClient(token),
		model:  openai.GPT3Dot5Turbo,
	}
	return cl
}

// CompletionOptions are the sum of all CompationOps
type CompletionOptions struct {
	rateLimit   time.Duration
	reqestorKey string
}

// CompletionOpt is a mutation of the completion request
type CompletionOpt func(c *CompletionOptions)

// WithRateLimit on requests
func WithRateLimit(reqestorKey string, d time.Duration) CompletionOpt {
	return func(c *CompletionOptions) {
		c.rateLimit = d
		c.reqestorKey = reqestorKey
	}
}

// RateLimit rate limit a request based on options
func (cl *OpenAI) RateLimit(o *CompletionOptions) error {
	now := time.Now()
	cl.Lock()
	defer cl.Unlock()
	if cl.globalRateLimit.After(now) {
		return fmt.Errorf("request too early, can request at %s", cl.globalRateLimit.Format(time.RFC3339))
	}
	if cl.nextRequestAt == nil {
		cl.nextRequestAt = make(map[string]time.Time)
	}
	if next, ok := cl.nextRequestAt[o.reqestorKey]; ok {
		if next.After(now) {
			return fmt.Errorf("request too early, can request at %s", next.Format(time.RFC3339))
		}
	}
	cl.nextRequestAt[o.reqestorKey] = now.Add(o.rateLimit)
	cl.globalRateLimit = now.Add(1 * time.Minute)
	return nil
}

// CreateCompletion makes a user request for completion
func (cl *OpenAI) CreateCompletion(ctx context.Context, message string, opts ...CompletionOpt) (string, error) {
	o := &CompletionOptions{}
	for _, opt := range opts {
		opt(o)
	}
	err := cl.RateLimit(o)
	if err != nil {
		return "The oracle is tired of your questions, take a break", nil
	}
	resp, err := cl.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: cl.model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: message},
		},
	})
	if err != nil {
		return "", err
	}
	log.Printf("openai returned %#v", resp)

	return resp.Choices[0].Message.Content, nil
}
