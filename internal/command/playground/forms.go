package playground

import (
	"strings"

	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/ui/field"
)

// queryLabels returns the three field labels+placeholders for a query mode.
func queryLabels(mode string) (labels [3]string, placeholders [3]string) {
	switch mode {
	case "list-objects":
		return [3]string{"Type", "Relation", "User"}, [3]string{"document", "viewer", "user:anne"}
	case "list-users":
		return [3]string{"Object", "Relation", "User type"}, [3]string{"document:roadmap", "viewer", "user"}
	default: // check
		return [3]string{"User", "Relation", "Object"}, [3]string{"user:anne", "viewer", "document:roadmap"}
	}
}

// buildQueryForm builds a 3-input query form for the given mode. Read values
// in order via Form.Values(): [a, b, c].
// buildQueryForm builds the query form. The ABAC context + contextual-tuples
// fields are hidden behind a toggle so the common query stays three fields;
// flipping the toggle reveals them (see advanceQueryForm's rebuild).
// Values() = [a, b, c, show_context, context_json?, contextual?].
func buildQueryForm(mode string, w int, showContext bool) *field.Form {
	labels, ph := queryLabels(mode)
	fields := []*field.Field{
		field.New(labels[0], ph[0]),
		field.New(labels[1], ph[1]),
		field.New(labels[2], ph[2]),
		field.NewToggle("Context + contextual tuples", "on", "off", showContext),
	}
	if showContext {
		fields = append(fields,
			field.New("Context (JSON)", "optional ABAC context"),
			field.New("Contextual tuples", "optional: user rel obj; …"),
		)
	}
	f := field.NewForm(fields...)
	f.SetWidth(w)
	return f
}

// buildCreateStoreForm builds the create-store form. Read with Values()[0].
func buildCreateStoreForm(w int) *field.Form {
	f := field.NewForm(field.New("Store name", "my-store"))
	f.SetWidth(w)
	return f
}

// buildWriteTupleForm builds the add-tuple form. The condition fields are
// optional (for `[user with …]` relations).
// Values() = [user, relation, object, condition, condition_context].
func buildWriteTupleForm(w int) *field.Form {
	f := field.NewForm(
		field.New("User", "user:anne"),
		field.New("Relation", "viewer"),
		field.New("Object", "document:roadmap"),
		field.New("Condition", "optional"),
		field.New("Condition context (JSON)", `{"grant_duration":"10m"}`),
	)
	f.SetWidth(w)
	return f
}

// buildWriteAssertionForm builds the add/edit-assertion form.
// Values() = [user, relation, object, expect("true"|"false")].
// Values() = [user, relation, object, expect, contextual_tuples, context_json].
func buildWriteAssertionForm(w int) *field.Form {
	f := field.NewForm(
		field.New("User", "user:anne"),
		field.New("Relation", "reader"),
		field.New("Object", "repo:openfga/openfga"),
		field.NewToggle("Expect", "Allowed", "Denied", true),
		field.New("Contextual tuples", "user:anne member team:eng; …"),
		field.New("Context (JSON)", `{"current_time":"…"}`),
	)
	f.SetWidth(w)
	return f
}

// authMethods is the ordered set of auth methods the profile form cycles
// through; authMethodIndex maps a method name to its position.
var authMethods = []string{
	config.AuthNone, config.AuthAPIToken, config.AuthClientCredentials, config.AuthPrivateKeyJWT,
}

func authMethodIndex(m string) int {
	for i, x := range authMethods {
		if x == m {
			return i
		}
	}
	return 0
}

// buildProfileForm builds the add/edit-profile form for a given auth method.
// Store and model stay auto-managed, so they are never fields here. The fields
// after the auth-method selector depend on the method; changing the selector
// rebuilds the form (see rebuildProfileForm). Field order:
//
//	add:  [name, api_url, auth_method, <method fields…>]
//	edit: [api_url, auth_method, <method fields…>]
func buildProfileForm(add bool, method string, w int) *field.Form {
	var fields []*field.Field
	if add {
		fields = append(fields, field.New("Profile name", "staging"))
	}
	fields = append(fields,
		field.New("API URL", config.DefaultAPIURL),
		field.NewSelect("Auth method", authMethods, authMethodIndex(method)),
	)
	switch method {
	case config.AuthAPIToken:
		fields = append(fields, field.New("API token", "token"))
	case config.AuthClientCredentials:
		fields = append(fields,
			field.New("Client ID", "client id"),
			field.New("Client secret", "client secret"),
			field.New("Token URL", "https://issuer/oauth/token"),
			field.New("Audience", "https://api.us1.fga.dev/"),
		)
	case config.AuthPrivateKeyJWT:
		fields = append(fields,
			field.New("Client ID", "client id"),
			field.New("Token URL", "https://issuer/oauth/token"),
			field.New("Audience (assertion)", "https://issuer/"),
			field.New("API audience", "https://api.us1.fga.dev/"),
			field.New("Key file", "/path/to/key.pem"),
			field.New("Signing method", "RS256"),
		)
	}
	f := field.NewForm(fields...)
	f.SetWidth(w)
	return f
}

// profileFormValues returns the SetValues slice pre-filling buildProfileForm for
// the given (existing) profile auth, matching the field order above.
func profileFormValues(add bool, apiURL string, a config.Auth) []string {
	method := a.Method
	if method == "" {
		method = config.AuthNone
	}
	var vals []string
	if add {
		vals = append(vals, "")
	}
	vals = append(vals, apiURL, method)
	switch method {
	case config.AuthAPIToken:
		vals = append(vals, a.Token)
	case config.AuthClientCredentials:
		vals = append(vals, a.ClientID, a.ClientSecret, a.TokenURL, a.Audience)
	case config.AuthPrivateKeyJWT:
		vals = append(vals, a.ClientID, a.TokenURL, a.Audience, a.APIAudience, a.KeyFile, a.SigningMethod)
	}
	return vals
}

// profileFromForm reads a completed profile form's values back into a Profile
// (auth included). Store/model are not touched here — the caller preserves them.
func profileFromForm(add bool, vals []string) (name string, p config.Profile) {
	get := func(i int) string {
		if i < len(vals) {
			return strings.TrimSpace(vals[i])
		}
		return ""
	}
	i := 0
	if add {
		name = get(i)
		i++
	}
	p.APIURL = get(i)
	i++
	method := get(i)
	i++
	p.Auth.Method = method
	switch method {
	case config.AuthAPIToken:
		p.Auth.Token = get(i)
	case config.AuthClientCredentials:
		p.Auth.ClientID, p.Auth.ClientSecret, p.Auth.TokenURL, p.Auth.Audience = get(i), get(i+1), get(i+2), get(i+3)
	case config.AuthPrivateKeyJWT:
		p.Auth.ClientID, p.Auth.TokenURL, p.Auth.Audience = get(i), get(i+1), get(i+2)
		p.Auth.APIAudience, p.Auth.KeyFile, p.Auth.SigningMethod = get(i+3), get(i+4), get(i+5)
	}
	if p.APIURL == "" {
		p.APIURL = config.DefaultAPIURL
	}
	return name, p
}
