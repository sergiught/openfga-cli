package agent

// ShowOutput is what `agent skill show` prints under --json.
type ShowOutput struct {
	Content string `json:"content"`
}

// InstallOutput is what `agent skill install` prints under --json.
type InstallOutput struct {
	// Path is the absolute path of the installed SKILL.md.
	Path string `json:"path"`
	// Changed is false when the file already held the same bytes.
	Changed bool `json:"changed"`
}

// OutputTypes maps each agent command's full path to what it prints under
// --json.
func OutputTypes() map[string]any {
	return map[string]any{
		"ofga agent skill show":    ShowOutput{},
		"ofga agent skill install": InstallOutput{},
	}
}
