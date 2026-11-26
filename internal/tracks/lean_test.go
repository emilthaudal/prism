package tracks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func leanDoc(t *testing.T, dir string) (doc struct {
	EnabledPlugins map[string]bool
	SkillOverrides map[string]string
	Permissions    struct{ Deny []string }
}) {
	t.Helper()
	t.Setenv("TRACKS_HOME", t.TempDir())
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
	doc := leanDoc(t, filepath.Join(home, "Developer", "tracks"))
	if len(doc.Permissions.Deny) != 1 || doc.Permissions.Deny[0] != "Artifact" {
		t.Errorf("Artifact is not denied: %+v", doc.Permissions)
	}
	if doc.SkillOverrides["desk"] != "off" || doc.SkillOverrides["lead"] != "off" || doc.SkillOverrides["track"] != "" {
		t.Errorf("skill overrides: %+v", doc.SkillOverrides)
	}
	if len(doc.EnabledPlugins) != 0 {
		t.Errorf("plugins switched off with none configured: %+v", doc.EnabledPlugins)
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
