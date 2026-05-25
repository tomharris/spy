package auth

// Credentials is the validated (xoxc-token, xoxd-cookie) pair the slack
// client uses to make API calls.
//
// Token is per-workspace; cookie is shared across every workspace the user
// is signed into on the same macOS Slack install (it's an account-level
// session cookie). See workspace.go for how these are produced and cached.
type Credentials struct {
	Token  string
	Cookie string
}
