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
	case "list-relations":
		return [3]string{"User", "Object", ""}, [3]string{"user:anne", "document:roadmap", ""}
	default: // check
		return [3]string{"User", "Relation", "Object"}, [3]string{"user:anne", "viewer", "document:roadmap"}
	}
}

// buildQueryForm builds the query form. The ABAC context + contextual-tuples
// fields are hidden behind a toggle so the common query stays three fields;
// flipping the toggle reveals them (see advanceQueryForm's rebuild).
// Values() = [a, b, c, show_context, context_json?, contextual?].
func buildQueryForm(mode string, w int, showContext bool) *field.Form {
	labels, ph := queryLabels(mode)
	fields := make([]*field.Field, 0, 4)
	for i := 0; i < queryFieldCount(mode); i++ {
		fields = append(fields, field.New(labels[i], ph[i]))
	}
	fields = append(fields, field.NewToggle("Context + Contextual Tuples", "on", "off", showContext))
	// The user/object fields sit at different positions per mode.
	switch mode {
	case "check":
		fields[0].WithValidate(vUser)
		fields[2].WithValidate(vObject)
	case "list-objects":
		fields[2].WithValidate(vUser)
	case "list-users":
		fields[0].WithValidate(vObject)
	case "list-relations":
		fields[0].WithValidate(vUser)
		fields[1].WithValidate(vObject)
	}
	if showContext {
		fields = append(fields,
			field.New("Context (JSON)", `{"current_time":"2023-01-01T00:00:00Z"}`).WithValidate(vJSON),
			field.New("Contextual Tuples", "user:anne member team:eng; user:bob viewer doc:1"),
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
		field.New("User", "user:anne").WithValidate(vUser),
		field.New("Relation", "viewer"),
		field.New("Object", "document:roadmap").WithValidate(vObject),
		field.New("Condition", "non_expired_grant"),
		field.New("Condition context (JSON)", `{"grant_duration":"10m"}`).WithValidate(vJSON),
	)
	f.SetWidth(w)
	return f
}

// buildWriteAssertionForm builds the add/edit-assertion form.
// Values() = [user, relation, object, expect, contextual_tuples, context_json].
func buildWriteAssertionForm(w int) *field.Form {
	f := field.NewForm(
		field.New("User", "user:anne").WithValidate(vUser),
		field.New("Relation", "reader"),
		field.New("Object", "repo:openfga/openfga").WithValidate(vObject),
		field.NewToggle("Expect", "Allowed", "Denied", true),
		field.New("Contextual Tuples", "user:anne member team:eng; user:bob viewer doc:1"),
		field.New("Context (JSON)", `{"current_time":"2023-01-01T00:00:00Z"}`).WithValidate(vJSON),
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
		field.New("API URL", config.DefaultAPIURL).WithValidate(vURL),
		field.NewSelect("Auth method", authMethods, authMethodIndex(method)),
	)
	// On edit we never pre-fill stored secrets, so the placeholder tells the
	// user that leaving a secret blank keeps the current one.
	secretPlaceholder := func(add bool, hint string) string {
		if add {
			return hint
		}
		return "leave blank to keep current"
	}
	switch method {
	case config.AuthAPIToken:
		fields = append(fields, field.New("API token", secretPlaceholder(add, "token")).Secret())
	case config.AuthClientCredentials:
		fields = append(fields,
			field.New("Client ID", "client id"),
			field.New("Client secret", secretPlaceholder(add, "client secret")).Secret(),
			field.New("Token URL", "https://issuer/oauth/token").WithValidate(vURL),
			field.New("Audience", "https://api.us1.fga.dev/"),
		)
	case config.AuthPrivateKeyJWT:
		fields = append(fields,
			field.New("Client ID", "client id"),
			field.New("Token URL", "https://issuer/oauth/token").WithValidate(vURL),
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
	// Secrets (token, client secret) are intentionally left blank rather than
	// pre-filled: they are never surfaced in the form. A blank secret field on
	// save means "keep the stored secret" (see the edit-profile handler).
	switch method {
	case config.AuthAPIToken:
		vals = append(vals, "")
	case config.AuthClientCredentials:
		vals = append(vals, a.ClientID, "", a.TokenURL, a.Audience)
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
