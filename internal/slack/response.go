package slack

// Response is implemented by every Slack API response type. Concrete
// responses embed BaseResponse to get these methods for free.
type Response interface {
	OK() bool
	SlackError() string
	NextCursor() string
}

// BaseResponse holds the fields every Slack response carries.
// Embed it in method-specific response structs.
type BaseResponse struct {
	Ok               bool             `json:"ok"`
	Error            string           `json:"error,omitempty"`
	ResponseMetadata ResponseMetadata `json:"response_metadata,omitempty"`
}

type ResponseMetadata struct {
	NextCursor string   `json:"next_cursor,omitempty"`
	Messages   []string `json:"messages,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
}

func (b *BaseResponse) OK() bool            { return b.Ok }
func (b *BaseResponse) SlackError() string  { return b.Error }
func (b *BaseResponse) NextCursor() string  { return b.ResponseMetadata.NextCursor }
