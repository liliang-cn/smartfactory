package mcptools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"smartfactory/internal/auth"
	"smartfactory/internal/tags"
)

// who is the holder of the token the call came with (the web layer verifies it and attaches
// them); a call without one is a visitor's, and visitors have no tools.
func who(req *mcp.CallToolRequest) auth.Who {
	if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil {
		return auth.Anonymous
	}
	if w, ok := req.Extra.TokenInfo.Extra["who"].(auth.Who); ok {
		return w
	}
	return auth.Anonymous
}

// guard lets a call through only when its caller holds need on the company it names. company
// nil means the tool is about no one company: the role alone decides.
func guard[In, Out any](need auth.Role, company func(In) string, h mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		w := who(req)
		var zero Out
		if company == nil {
			if w.Role < need {
				return nil, zero, fmt.Errorf("this needs %s token; yours is %s", article(need), w.Role)
			}
		} else if c := company(in); !w.Can(c, need) {
			if !w.In(c) {
				return nil, zero, fmt.Errorf("your token does not cover company %q; it covers %s", c, scope(w))
			}
			return nil, zero, fmt.Errorf("this needs %s token for %q; yours is %s", article(need), c, w.Role)
		}
		return h(ctx, req, in)
	}
}

// everywhere lets a call through only for a caller holding need on every company: what it
// changes is shared by all of them (an industry pack).
func everywhere[In, Out any](need auth.Role, h mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var zero Out
		if w := who(req); w.Role < need || !w.Everywhere() {
			return nil, zero, fmt.Errorf("this changes what every company shares: it needs %s token for every company", article(need))
		}
		return h(ctx, req, in)
	}
}

// article is a role with its indefinite article: "an operator", "a member".
func article(r auth.Role) string {
	if strings.ContainsRune("aeiou", rune(r.String()[0])) {
		return "an " + r.String()
	}
	return "a " + r.String()
}

func scope(w auth.Who) string {
	if w.Everywhere() {
		return "every company"
	}
	return strings.Join(w.Companies, ", ")
}

// visibleSources is the adapters with only the assets the caller's token covers; an adapter
// feeding none of them is left out.
func visibleSources(w auth.Who, all []tags.SourceStatus) []tags.SourceStatus {
	if w.Everywhere() {
		return all
	}
	out := []tags.SourceStatus{}
	for _, s := range all {
		var keep []string
		for _, t := range s.Targets {
			if w.In(strings.SplitN(t, "/", 2)[0]) {
				keep = append(keep, t)
			}
		}
		if len(keep) > 0 {
			s.Targets = keep
			s.Labels = nil
			out = append(out, s)
		}
	}
	return out
}
