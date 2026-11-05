package optimize

import (
	"fmt"
	"maps"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

func usersetSubjects(checks []checkKey, object, rel string) (users, usersets []string) {
	for _, c := range checks {
		if c.object != object || c.relation != rel {
			continue
		}
		if strings.Contains(c.user, "#") {
			usersets = append(usersets, c.user)
		} else {
			users = append(users, c.user)
		}
	}
	return users, usersets
}

func TestAllChecks_IncludesUsersetSubjects(t *testing.T) {
	m := loadInline(t, "model\n  schema 1.1\ntype user\ntype t0\n  relations\n    define parent: [t0]\n    define r0: [user]\n    define r1: r0\ntype t1\n  relations\n    define parent: [t1]\n    define s0: [user]\n")
	checks := allChecks(m.Proto)
	users, sets := usersetSubjects(checks, "t0:1", "r0")
	wantUsers := []string{"user:1", "user:2", "user:3", "user:4", "user:nobody"}
	if !slices.Equal(users, wantUsers) {
		t.Errorf("users = %v, want %v", users, wantUsers)
	}
	wantSets := []string{"t0:1#parent", "t0:1#r0", "t0:1#r1", "t1:1#parent", "t1:1#s0"}
	if !slices.Equal(sets, wantSets) {
		t.Errorf("usersets = %v, want %v", sets, wantSets)
	}
	for _, c := range checks {
		if _, s := usersetSubjects(checks, c.object, c.relation); len(s) > maxUsersetSubjects {
			t.Errorf("%s#%s has %d userset subjects", c.object, c.relation, len(s))
		}
	}
	if again := allChecks(m.Proto); !slices.Equal(checks, again) {
		t.Error("allChecks is not deterministic")
	}
}

func TestAllChecks_CapsUsersetSubjectsOnly(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("model\n  schema 1.1\ntype user\n")
	for ti := range 4 {
		fmt.Fprintf(&sb, "type t%d\n  relations\n    define parent: [t%d]\n", ti, ti)
		for ri := range 4 {
			fmt.Fprintf(&sb, "    define r%d: [user]\n", ri)
		}
	}
	m := loadInline(t, sb.String())
	users, sets := usersetSubjects(allChecks(m.Proto), "t0:1", "r0")
	if len(users) != 5 {
		t.Errorf("user subjects = %d, want 5", len(users))
	}
	if len(sets) != maxUsersetSubjects {
		t.Fatalf("userset subjects = %d, want %d", len(sets), maxUsersetSubjects)
	}
	want := []string{"t0:1#parent", "t0:1#r0", "t0:1#r1", "t0:1#r2", "t0:1#r3"}
	if !slices.Equal(sets[:5], want) {
		t.Errorf("first usersets = %v, want %v", sets[:5], want)
	}
}

func TestGenTuples_ConditionFalseBeforeWildcard(t *testing.T) {
	m := loadInline(t, hdr+"    define viewer: [user with cx, user:*]\n"+"\ncondition cx(x: int) {\n  x < 10\n}\n")
	for seed := uint64(1); seed <= 20; seed++ {
		gen := func() []string {
			r := rand.New(rand.NewPCG(seed, 0))  //nolint:gosec // deterministic test generator
			rc := rand.New(rand.NewPCG(seed, 1)) //nolint:gosec // conditions and wildcards
			var out []string
			for _, tk := range genTuples(r, rc, m.Proto, 30) {
				s := tk.GetObject() + "#" + tk.GetRelation() + "@" + tk.GetUser()
				if c := tk.GetCondition(); c != nil {
					s += fmt.Sprintf(" %s %v", c.GetName(), c.GetContext().AsMap()["x"])
				}
				out = append(out, s)
			}
			return out
		}
		got := gen()
		if !slices.Equal(got, gen()) {
			t.Fatalf("seed %d: genTuples is not deterministic", seed)
		}
		found := false
		for i, s := range got {
			if !strings.HasPrefix(s, "doc:1#viewer@user:1 cx ") {
				continue
			}
			var x float64
			if _, err := fmt.Sscanf(s, "doc:1#viewer@user:1 cx %g", &x); err != nil || x < 10 {
				continue
			}
			if slices.Contains(got[i+1:], "doc:1#viewer@user:*") {
				found = true
			}
		}
		if !found {
			t.Errorf("seed %d: no condition-false tuple before a wildcard in %v", seed, got)
		}
	}
}

func TestAnswer_ErrorsCompareAsValues(t *testing.T) {
	k := checkKey{"doc:1", "viewer", "user:1"}
	if !maps.Equal(map[checkKey]answer{k: checkErr}, map[checkKey]answer{k: checkErr}) {
		t.Error("equal error answers compare unequal")
	}
	if maps.Equal(map[checkKey]answer{k: checkErr}, map[checkKey]answer{k: allow}) {
		t.Error("error equals allow")
	}
	if checkErr.String() != "error" || allow.String() != "allow" || deny.String() != "deny" {
		t.Error("bad String()")
	}
}
