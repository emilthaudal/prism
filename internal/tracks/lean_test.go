package tracks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type leanSettingsDoc struct {
	EnabledPlugins map[string]bool
	SkillOverrides map[string]string
	Permissions    *struct{ Deny []string }
}

func leanDoc(t *testing.T, dir string) leanSettingsDoc {
	t.Helper()
	return leanDocFor(t, dir, "")
}

// leanDocFor writes the desk settings for track lean, whose card is card when it is not empty.
func leanDocFor(t *testing.T, dir, card string) (doc leanSettingsDoc) {
	t.Helper()
	t.Setenv("TRACKS_HOME", t.TempDir())
	if card != "" {
		writeFile(t, cardPath("lean"), card)
	}
	path, err := writeDeskSettings("lean", "/bin/tracks", dir, "parent", "/track lean")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("settings: %v %s", err, b)
	}
	return doc
}

func TestADeskSessionDropsWhatItDoesNotUse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TRACKS_LEAN_SKILLS_OFF", "")
	t.Setenv("TRACKS_LEAN_PLUGINS_OFF", "")
	t.Setenv("TRACKS_DIR_PLUGINS", "")
	doc := leanDoc(t, filepath.Join(home, "Developer", "tracks"))
	if doc.Permissions == nil || len(doc.Permissions.Deny) != 1 || doc.Permissions.Deny[0] != "Artifact" {
		t.Errorf("Artifact is not denied: %+v", doc.Permissions)
	}
	if doc.SkillOverrides["desk"] != "off" || doc.SkillOverrides["lead"] != "off" || doc.SkillOverrides["track"] != "" {
		t.Errorf("skill overrides: %+v", doc.SkillOverrides)
	}
	if len(doc.EnabledPlugins) != 0 {
		t.Errorf("plugins switched off with none configured: %+v", doc.EnabledPlugins)
	}
}

func TestATrackOnAnArtifactKeepsTheArtifactTool(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TRACKS_LEAN_SKILLS_OFF", "")
	t.Setenv("TRACKS_LEAN_PLUGINS_OFF", "")
	dir := filepath.Join(home, "Developer")
	card := func(goal, links string) string {
		return "---\nname: lean\nmode: background\nlinks: " + links + "\n---\n## Goal\n" + goal + "\n\n## Where it is\nNot started.\n"
	}
	for _, c := range []struct{ why, goal, links string }{
		{"goal", "In the dashboard (https://claude.ai/artifact/2PH5FEeiXs1pUaQx2hfTep): order by sequence", ""},
		{"code link", "Fix https://claude.ai/code/artifact/abc", ""},
		{"links", "Order the timeline", "EMI-1, https://claude.ai/artifact/abc"},
	} {
		if doc := leanDocFor(t, dir, card(c.goal, c.links)); doc.Permissions != nil {
			t.Errorf("%s: Artifact denied for an artifact track: %+v", c.why, doc.Permissions)
		}
	}
	if doc := leanDocFor(t, dir, card("Fix the pane on https://claude.ai/new", "")); doc.Permissions == nil {
		t.Error("Artifact kept for a track that names no artifact")
	}
}

func TestTheEnvironmentAddsSkillsAndPluginsToTurnOff(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TRACKS_LEAN_SKILLS_OFF", "pdf, xlsx,")
	t.Setenv("TRACKS_LEAN_PLUGINS_OFF", "datadog@claude-plugins-official")
	doc := leanDoc(t, filepath.Join(home, "Developer", "tracks"))
	if doc.SkillOverrides["pdf"] != "off" || doc.SkillOverrides["xlsx"] != "off" || doc.SkillOverrides["desk"] != "off" {
		t.Errorf("skill overrides: %+v", doc.SkillOverrides)
	}
	if _, ok := doc.SkillOverrides[""]; ok {
		t.Errorf("an empty entry became a skill: %+v", doc.SkillOverrides)
	}
	if on, ok := doc.EnabledPlugins["datadog@claude-plugins-official"]; !ok || on {
		t.Errorf("datadog stays on: %+v", doc.EnabledPlugins)
	}
}

func TestADirPluginStaysOnOnlyUnderItsDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TRACKS_LEAN_SKILLS_OFF", "")
	t.Setenv("TRACKS_LEAN_PLUGINS_OFF", "")
	t.Setenv("TRACKS_DIR_PLUGINS", "~/Developer/acme=acme@acme, ~/Developer/acme=ops@acme,/elsewhere=ops@acme")
	doc := leanDoc(t, filepath.Join(home, "Developer", "acme", "shop-api"))
	if len(doc.EnabledPlugins) != 0 {
		t.Errorf("a plugin is off under its own dir: %+v", doc.EnabledPlugins)
	}
	doc = leanDoc(t, filepath.Join(home, "Developer", "acme-other"))
	if on, ok := doc.EnabledPlugins["acme@acme"]; !ok || on {
		t.Errorf("acme stays on outside its dir: %+v", doc.EnabledPlugins)
	}
	if on, ok := doc.EnabledPlugins["ops@acme"]; !ok || on {
		t.Errorf("ops stays on outside its dirs: %+v", doc.EnabledPlugins)
	}
	doc = leanDoc(t, "/elsewhere/repo")
	if _, ok := doc.EnabledPlugins["ops@acme"]; ok {
		t.Errorf("ops is off under its second dir: %+v", doc.EnabledPlugins)
	}
}
