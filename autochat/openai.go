package autochat

import (
	"context"
	"log"

	openai "github.com/sashabaranov/go-openai"
)

// OpenAI client
type OpenAI struct {
	client openai.Client
	model  string
}

// NewOpenAI client with given token
func NewOpenAI(token string) *OpenAI {
	cl := &OpenAI{
		client: *openai.NewClient(token),
		model:  openai.GPT3Dot5Turbo,
	}
	return cl
}

// CreateCompletion makes a user request for completion
func (cl *OpenAI) CreateCompletion(ctx context.Context, message string) (string, error) {
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
