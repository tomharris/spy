package slack

import "context"

// Paginate walks Slack's cursor pagination until exhausted.
//
//	users, err := slack.Paginate(ctx, client, "users.list", nil,
//	    func() *UsersListResponse { return &UsersListResponse{} },
//	    func(p *UsersListResponse) []User { return p.Members },
//	)
//
// Caller provides:
//   - fresh:   constructs an empty page response (typically `&FooResponse{}`)
//   - extract: pulls the slice of items out of a page response
//
// Default limit=200 if not set in params.
func Paginate[Page Response, Item any](
	ctx context.Context,
	c *Client,
	method string,
	params map[string]any,
	fresh func() Page,
	extract func(Page) []Item,
) ([]Item, error) {
	if params == nil {
		params = map[string]any{}
	}
	if _, ok := params["limit"]; !ok {
		params["limit"] = 200
	}

	var all []Item
	for {
		page := fresh()
		if err := c.Call(ctx, method, params, page); err != nil {
			return all, err
		}
		all = append(all, extract(page)...)

		cursor := page.NextCursor()
		if cursor == "" {
			return all, nil
		}
		params["cursor"] = cursor
	}
}
