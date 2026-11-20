package auth0

import (
	"encoding/json"
	"fmt"
	"time"
)

// toMap renders an SDK value as the JSON object Auth0 sent, keeping the
// fields the SDK has no struct field for.
func toMap(v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}

	return m, nil
}

// event is a snapshot event before it has an id.
type event struct {
	typ, subject string
	object       map[string]any
	at           time.Time
}

// envelope completes e as a CloudEvents-style event.
func (p *Provider) envelope(e event, seq int) map[string]any {
	return map[string]any{
		"specversion": "1.0",
		"type":        e.typ,
		"source":      "urn:auth0:" + p.source,
		"id":          fmt.Sprintf("snap_%06d", seq),
		"time":        e.at.UTC().Format(time.RFC3339),
		"a0tenant":    p.tenant,
		"subject":     e.subject,
		"data": map[string]any{
			"object":  e.object,
			"context": map[string]any{"tenant": map[string]any{"id": p.tenant}},
		},
	}
}

func orgRef(org map[string]any) map[string]any {
	return map[string]any{"id": org["id"], "name": org["name"]}
}

// orgConnectionObject reshapes an enabled-connection record into the payload
// of organization.connection.added.
func orgConnectionObject(org, raw map[string]any) map[string]any {
	conn, _ := raw["connection"].(map[string]any)

	obj := map[string]any{
		"organization": orgRef(org),
		"connection":   map[string]any{"id": raw["connection_id"], "name": conn["name"], "strategy": conn["strategy"]},
		"is_enabled":   true,
	}

	for k, v := range raw {
		if k != "connection_id" && k != "connection" {
			obj[k] = v
		}
	}

	return obj
}
