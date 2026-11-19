package mapping

import "github.com/openfga/mapper/language"

// Shape is what a tuple looks like with its ids removed.
type Shape struct{ UserType, Relation, ObjectType string } // UserType is "user", "user:*" or "group#member"

// String renders the shape as "user member organization".
func (s Shape) String() string { return s.UserType + " " + s.Relation + " " + s.ObjectType }

// ShapeOf is the shape of a concrete tuple. A wildcard user has UserType
// "user:*", a shape of its own, so it never shares one with concrete users.
func ShapeOf(t language.Tuple) Shape {
	userType, _ := modelUserType(t.User)
	objectType, _ := literalObjectType(t.Object)
	return Shape{UserType: userType, Relation: t.Relation, ObjectType: objectType}
}

// RuleShapes returns the shapes of the tuples r writes (its own and its
// iterator's), deduplicated in template order, and how many written
// templates have no shape (templated type or relation). Tuples with action
// delete, a rule with Action "delete", and tuple_filters contribute nothing.
func RuleShapes(r Rule) (shapes []Shape, unshaped int) {
	if r.Action == "delete" {
		return nil, 0
	}
	templates := r.Tuples
	if r.Iterator != nil {
		templates = append(append([]Tuple{}, templates...), r.Iterator.Tuples...)
	}
	seen := map[Shape]bool{}
	for _, t := range templates {
		if t.Action == "delete" {
			continue
		}
		userType, userOK := modelUserType(t.User)
		objectType, objectOK := literalObjectType(t.Object)
		if !userOK || !objectOK || !fixed(t.Relation) {
			unshaped++
			continue
		}
		s := Shape{UserType: userType, Relation: t.Relation, ObjectType: objectType}
		if !seen[s] {
			seen[s] = true
			shapes = append(shapes, s)
		}
	}
	return shapes, unshaped
}
