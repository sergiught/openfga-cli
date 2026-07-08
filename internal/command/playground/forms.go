package playground

import "github.com/sergiught/openfga-cli/internal/ui/field"

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
func buildQueryForm(mode string, w int) *field.Form {
	labels, ph := queryLabels(mode)
	f := field.NewForm(
		field.New(labels[0], ph[0]),
		field.New(labels[1], ph[1]),
		field.New(labels[2], ph[2]),
	)
	f.SetWidth(w)
	return f
}

// buildCreateStoreForm builds the create-store form. Read with Values()[0].
func buildCreateStoreForm(w int) *field.Form {
	f := field.NewForm(field.New("Store name", "my-store"))
	f.SetWidth(w)
	return f
}

// buildWriteTupleForm builds the add-tuple form. Values() = [user, relation, object].
func buildWriteTupleForm(w int) *field.Form {
	f := field.NewForm(
		field.New("User", "user:anne"),
		field.New("Relation", "viewer"),
		field.New("Object", "document:roadmap"),
	)
	f.SetWidth(w)
	return f
}
