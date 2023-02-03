package twitch

// CreateClipResponse is the response from twitch on creating a clip
type CreateClipResponse struct {
	Data []struct {
		EditURL string `json:"edit_url"`
		ID      string `json:"id"`
	} `json:"data"`
}

// APIError is the standard error struct from twitch
type APIError struct {
	Error   string `json:"error"`
	Status  int    `json:"status"`
	Message string `json:"message"`
}
