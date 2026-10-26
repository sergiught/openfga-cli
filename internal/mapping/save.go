package mapping

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"

	"github.com/openfga/mapper/language"
	"gopkg.in/yaml.v3"

	"github.com/sergiught/openfga-cli/internal/atomicfile"
)

// Render returns the bytes Save would write for doc, and whether they differ
// from the file in meaning (not just formatting).
func (f *File) Render(doc *Document) ([]byte, bool, error) {
	if !f.Editable {
		return nil, false, errors.New(f.Path + ": " + f.Reason)
	}
	updated := marshalTree(doc)
	merged := mergeMapping(f.root.Content[0], updated, true)
	reorderVariables(merged, updated)
	out, err := encode(&yaml.Node{
		Kind: yaml.DocumentNode, HeadComment: f.root.HeadComment,
		FootComment: f.root.FootComment, Content: []*yaml.Node{merged},
	})
	if err != nil {
		return nil, false, err
	}
	// Meaning, not bytes: re-encoding can change indentation even when nothing
	// was edited, and that alone is no reason to touch the file.
	before, err1 := canonicalOf(f.Src)
	after, err2 := canonicalOf(out)
	changed := err1 != nil || err2 != nil || before != after
	// The merge must not lose or invent anything: what it wrote has to mean
	// what the document means.
	want, err := Marshal(doc)
	if err != nil {
		return nil, false, err
	}
	wantCanon, err := canonicalOf(want)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", f.Path, err)
	}
	if err2 != nil || wantCanon != after {
		return nil, false, fmt.Errorf("%s: could not keep the file's layout; %w", f.Path, ErrLayout)
	}
	return f.withEndings(out), changed, nil
}

// withEndings gives out the file's own line endings and BOM.
func (f *File) withEndings(out []byte) []byte {
	if f.crlf {
		out = bytes.ReplaceAll(out, []byte("\n"), []byte("\r\n"))
	}
	if f.bom {
		out = append(append([]byte{}, utf8BOM...), out...)
	}
	return out
}

// ErrLayout is why Render cannot keep a file's layout: what the merge wrote
// would not mean what the edit means. SaveReformatted writes it anyway.
var ErrLayout = errors.New("the saved file would differ from the edit")

// ErrChangedOnDisk is why Save refuses to write: the file on disk is no longer
// what was loaded or last saved, and writing would discard someone's edit.
var ErrChangedOnDisk = errors.New("changed on disk since it was loaded; reload it before saving")

// Save writes Render's output atomically when it changed the file's meaning,
// and reports whether it wrote anything. It writes documents mapper would
// reject too, so work in progress can be saved. It refuses, writing nothing,
// when the file on disk is no longer what f loaded or last saved. After a
// write f describes the file now on disk and f.Doc is a new value: keep
// editing f.Doc, not doc.
func (f *File) Save(doc *Document) (bool, error) { return f.save(doc, false, false) }

// SaveReformatted is Save writing Marshal(doc) instead of Render's
// layout-keeping bytes, for a document Render cannot lay out: comments and key
// order are lost, while CRLF line endings and a BOM are kept. It keeps Save's
// changed-on-disk check.
func (f *File) SaveReformatted(doc *Document) (bool, error) { return f.save(doc, false, true) }

// Overwrite is Save without the changed-on-disk check, for "save over it". It
// writes even when the file is missing or the edit changes nothing, and
// reports whether it wrote.
func (f *File) Overwrite(doc *Document) (bool, error) { return f.save(doc, true, false) }

// ChangedOnDisk reports whether the file on disk is no longer what f loaded or
// last saved. A file that is gone counts as changed.
func (f *File) ChangedOnDisk() (bool, error) {
	cur, err := os.ReadFile(f.Path) //nolint:gosec // the user's own file
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return !bytes.Equal(cur, f.disk), nil
}

func (f *File) save(doc *Document, force, reformat bool) (bool, error) {
	var (
		out     []byte
		changed bool
		err     error
	)
	if reformat {
		if !f.Editable {
			return false, errors.New(f.Path + ": " + f.Reason)
		}
		out, err = Marshal(doc)
		out, changed = f.withEndings(out), true
	} else {
		out, changed, err = f.Render(doc)
	}
	if err != nil || (!changed && !force) {
		return false, err
	}
	if !force {
		if changed, err := f.ChangedOnDisk(); err != nil {
			return false, err
		} else if changed {
			return false, fmt.Errorf("%s %w", f.Path, ErrChangedOnDisk)
		}
	}
	// Write the file a link points at: the rename would otherwise replace the
	// link itself and leave its target as it was. f.Path stays the user's name.
	target := f.Path
	if resolved, err := filepath.EvalSymlinks(f.Path); err == nil {
		target = resolved
	} else if dest, err := os.Readlink(f.Path); err == nil {
		// A link whose target is gone: write the target back, keeping the link.
		if !filepath.IsAbs(dest) {
			dest = filepath.Join(filepath.Dir(f.Path), dest)
		}
		target = dest
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(target); err == nil { //nolint:gosec // a link's target, as the user wrote it
		mode = st.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil { //nolint:gosec // a link's target, as the user wrote it
		return false, err
	}
	af, err := atomicfile.Create(target, mode)
	if err != nil {
		return false, err
	}
	defer af.Abort()
	if _, err := af.Write(out); err != nil {
		return false, err
	}
	if err := af.Commit(); err != nil {
		return false, err
	}
	// Describe what is on disk now, so the next Save compares against it.
	f.disk = out
	// A draft: the saved document may not validate, and f must still describe
	// it so the next Save can render.
	fresh, err := LoadDraftBytes(f.Path, out)
	if err != nil {
		return true, err
	}
	*f = *fresh
	return true, nil
}

// Create writes doc to path, which must not exist, and returns it loaded as a
// draft. A path that exists fails with an error wrapping fs.ErrExist and
// writes nothing. The commit links the file into place, so something created
// between the Lstat and the commit is not replaced; only on a filesystem that
// cannot hard link does that race remain, and Save's own check covers every
// later write.
func Create(path string, doc *Document) (*File, error) {
	if _, err := os.Lstat(path); err == nil {
		return nil, fmt.Errorf("%s: %w", path, fs.ErrExist)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	out, err := Marshal(doc)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	af, err := atomicfile.Create(path, 0o644)
	if err != nil {
		return nil, err
	}
	defer af.Abort()
	if _, err := af.Write(out); err != nil {
		return nil, err
	}
	if err := af.CommitNew(); err != nil {
		return nil, err
	}
	return LoadDraftBytes(path, out)
}

func canonicalOf(src []byte) (string, error) {
	c, err := language.Parse(src)
	if err != nil {
		return "", err
	}
	return canonical(c), nil
}

// mergeNode returns upd with orig's comments, key order and (for unchanged
// scalars) the original node itself, so its quoting survives.
func mergeNode(orig, upd *yaml.Node) *yaml.Node {
	if orig == nil {
		return upd
	}
	if orig.Kind != upd.Kind {
		copyComments(orig, upd)
		return upd
	}
	switch upd.Kind {
	case yaml.ScalarNode:
		if sameScalar(orig, upd) {
			return orig
		}
		copyComments(orig, upd)
		return upd
	case yaml.MappingNode:
		return mergeMapping(orig, upd, false)
	case yaml.SequenceNode:
		return mergeSequence(orig, upd)
	}
	return upd
}

// styleFor is the style the merged collection is written in: orig's, except
// that an empty one (`rules: []`) is flow style only because it has to be, and
// would put every item added to it on one line.
func styleFor(orig, upd *yaml.Node) yaml.Style {
	if len(orig.Content) == 0 && len(upd.Content) > 0 {
		return upd.Style
	}
	return orig.Style
}

// mergeMapping keeps orig's key order. root is the file's top-level mapping,
// whose last key carries the comment closing the file.
func mergeMapping(orig, upd *yaml.Node, root bool) *yaml.Node {
	out := &yaml.Node{Kind: yaml.MappingNode, Tag: orig.Tag, Style: styleFor(orig, upd)}
	copyComments(orig, out)
	at := map[string]int{}
	for j := 0; j+1 < len(upd.Content); j += 2 {
		at[upd.Content[j].Value] = j
	}
	seen := map[string]bool{}
	var orphanFoot string
	for j := 0; j+1 < len(orig.Content); j += 2 {
		k := orig.Content[j].Value
		u, ok := at[k]
		if !ok {
			// A deleted key's comments go with it, except a comment closing the
			// file, which yaml.v3 hangs on the last key.
			if root && j+2 == len(orig.Content) {
				orphanFoot = orig.Content[j].FootComment
			}
			continue
		}
		seen[k] = true
		key, val := orig.Content[j], mergeNode(orig.Content[j+1], upd.Content[u+1])
		if c := orig.Content[j+1]; c.Kind == yaml.SequenceNode && len(c.Content) == 0 &&
			c.LineComment != "" && len(val.Content) > 0 && key.LineComment == "" {
			// yaml.v3 drops a comment on a flow list that becomes a block: the
			// key can still carry it.
			moved := *key
			moved.LineComment = c.LineComment
			key = &moved
			if val != c {
				v := *val
				v.LineComment = ""
				val = &v
			}
		}
		out.Content = append(out.Content, key, val)
	}
	for j := 0; j+1 < len(upd.Content); j += 2 {
		if !seen[upd.Content[j].Value] {
			out.Content = append(out.Content, upd.Content[j], upd.Content[j+1])
		}
	}
	if orphanFoot != "" && len(out.Content) > 0 {
		// Copy: the key node belongs to the File and Render may run again.
		last := *out.Content[len(out.Content)-2]
		if last.FootComment != "" {
			last.FootComment += "\n"
		}
		last.FootComment += orphanFoot
		out.Content[len(out.Content)-2] = &last
	}
	return out
}

// mergeSequence pairs items by key: the `name` of a mapping that has one, else
// the item's canonical encoding. Equal keys pair in order. Items left over on
// both sides pair by order only when there are as many of one as the other (a
// pure rename or edit, with or without reordering); otherwise they stay
// unpaired, because a lost comment beats one attached to the wrong item.
func mergeSequence(orig, upd *yaml.Node) *yaml.Node {
	out := &yaml.Node{Kind: yaml.SequenceNode, Tag: orig.Tag, Style: styleFor(orig, upd)}
	copyComments(orig, out)
	pair := make([]*yaml.Node, len(upd.Content))
	queue := map[string][]int{}
	for i, it := range orig.Content {
		queue[itemKey(it)] = append(queue[itemKey(it)], i)
	}
	used := make([]bool, len(orig.Content))
	var left []int
	for i, u := range upd.Content {
		k := itemKey(u)
		if q := queue[k]; len(q) > 0 {
			pair[i], used[q[0]] = orig.Content[q[0]], true
			queue[k] = q[1:]
		} else {
			left = append(left, i)
		}
	}
	var origLeft []int
	for i := range orig.Content {
		if !used[i] {
			origLeft = append(origLeft, i)
		}
	}
	if len(origLeft) == len(left) {
		for k, i := range left {
			pair[i] = orig.Content[origLeft[k]]
		}
	}
	for i, u := range upd.Content {
		out.Content = append(out.Content, mergeNode(pair[i], u))
	}
	return out
}

// itemKey identifies a sequence item across the original and the update.
func itemKey(n *yaml.Node) string {
	if name := nameOf(n); name != "" {
		return "n:" + name
	}
	var v any
	if err := n.Decode(&v); err != nil {
		return "x:" + n.Value
	}
	b, err := yaml.Marshal(v)
	if err != nil {
		return "x:" + n.Value
	}
	return "v:" + string(b)
}

// sameScalar reports whether orig already says what upd says, so the original
// spelling (`~`, `True`, `0x1F`, quoting) can stay. An empty null is never
// kept: yaml.v3 writes it back in a flow mapping as an empty string.
func sameScalar(orig, upd *yaml.Node) bool {
	if orig.ShortTag() == "!!null" && orig.Value == "" {
		return false
	}
	var a, b any
	return orig.Decode(&a) == nil && upd.Decode(&b) == nil && reflect.DeepEqual(a, b)
}

// reorderVariables gives each rule's `variables` the order of the updated
// document: it is the order they are evaluated in, so unlike every other key
// the user's reordering must survive. merged and updated list the same rules in
// the same order.
func reorderVariables(merged, updated *yaml.Node) {
	mr, ur := keyValue(merged, "rules"), keyValue(updated, "rules")
	if mr == nil || ur == nil || len(mr.Content) != len(ur.Content) {
		return
	}
	for i := range ur.Content {
		mv, uv := keyValue(mr.Content[i], "variables"), keyValue(ur.Content[i], "variables")
		if mv == nil || uv == nil || mv.Kind != yaml.MappingNode || len(mv.Content) != len(uv.Content) {
			continue
		}
		at := map[string]int{}
		for j := 0; j+1 < len(mv.Content); j += 2 {
			at[mv.Content[j].Value] = j
		}
		var content []*yaml.Node
		for j := 0; j+1 < len(uv.Content); j += 2 {
			p, ok := at[uv.Content[j].Value]
			if !ok {
				content = nil
				break
			}
			content = append(content, mv.Content[p], mv.Content[p+1])
		}
		if content != nil {
			mv.Content = content
		}
	}
}

func keyValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for j := 0; j+1 < len(m.Content); j += 2 {
		if m.Content[j].Value == key {
			return m.Content[j+1]
		}
	}
	return nil
}

func nameOf(n *yaml.Node) string {
	if n.Kind != yaml.MappingNode {
		return ""
	}
	for j := 0; j+1 < len(n.Content); j += 2 {
		if n.Content[j].Value == "name" {
			return n.Content[j+1].Value
		}
	}
	return ""
}

func copyComments(from, to *yaml.Node) {
	to.HeadComment, to.LineComment, to.FootComment = from.HeadComment, from.LineComment, from.FootComment
}

// usesAliases reports whether the tree has an anchor, an alias or a merge key.
// Merging edits into an alias would rewrite every place it is used, so such a
// file is left to hand editing.
func usesAliases(n *yaml.Node) bool {
	if n.Kind == yaml.AliasNode || n.Anchor != "" || (n.Kind == yaml.ScalarNode && n.ShortTag() == "!!merge") {
		return true
	}
	for _, c := range n.Content {
		if usesAliases(c) {
			return true
		}
	}
	return false
}
