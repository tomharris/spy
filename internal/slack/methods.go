package slack

// Verb is the HTTP verb used to invoke a Slack API method.
type Verb string

const (
	GET  Verb = "GET"
	POST Verb = "POST"
)

// verbByMethod records the HTTP verb each Slack method expects. Slack's
// public docs are inconsistent and several undocumented client.* and
// users.prefs.* endpoints require POST despite being semantic reads —
// the set of methods that need POST is fixed and known.
var verbByMethod = map[string]Verb{
	// auth & introspection
	"auth.test": GET,

	// conversations
	"conversations.list":    GET,
	"conversations.history": GET,
	"conversations.replies": GET,
	"conversations.open":    POST,

	// users
	"users.list":      GET,
	"users.prefs.get": POST,

	// activity / counts (undocumented client endpoints, POST required)
	"client.counts": POST,

	// stars / pins / saved
	"stars.list": GET,
	"pins.list":  GET,
	"saved.list": POST,

	// search
	"search.messages": GET,

	// write methods
	"chat.postMessage": POST,
	"chat.update":      POST,
	"chat.delete":      POST,
	"reactions.add":    POST,
	"reactions.remove": POST,
	"files.upload":     POST,

	// drafts (undocumented)
	"drafts.list":   GET,
	"drafts.create": POST,
	"drafts.delete": POST,
	"drafts.update": POST,
}

// MethodVerb returns the registered verb for a Slack method.
func MethodVerb(method string) (Verb, bool) {
	v, ok := verbByMethod[method]
	return v, ok
}
