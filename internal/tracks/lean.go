package tracks

import (
	"os"
	"path/filepath"
	"strings"
)

// leanSkillsOff are skills a track's session never uses and whose listing every call rereads:
// the desk's and the lead's own skills. TRACKS_LEAN_SKILLS_OFF adds more, comma-separated.
var leanSkillsOff = []string{"desk", "lead"}

// envList reads a comma-separated list from the environment, dropping empty entries.
func envList(name string) []string {
	var out []string
	for _, f := range strings.Split(os.Getenv(name), ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// underDir says whether dir is root or inside it. root may start with ~/.
func underDir(dir, root string) bool {
	root = filepath.Clean(expandHome(root))
	dir = filepath.Clean(dir)
	return dir == root || strings.HasPrefix(dir, root+string(filepath.Separator))
}

// leanSettings are the settings keys that cut a track session's start-up context: skills and
// plugins it does not use, and the Artifact tool. TRACKS_LEAN_PLUGINS_OFF lists plugins to turn
// off everywhere, comma-separated, as name@marketplace. TRACKS_DIR_PLUGINS lists DIR=plugin
// pairs, comma-separated: such a plugin stays on for tracks whose dir is under DIR (one of them,
// when it is named more than once) and is off everywhere else. They apply to the session's
// process, so its subagents get them too.
func leanSettings(dir string) map[string]any {
	plugins := map[string]bool{}
	for _, p := range envList("TRACKS_LEAN_PLUGINS_OFF") {
		plugins[p] = false
	}
	keep := map[string]bool{}
	for _, f := range envList("TRACKS_DIR_PLUGINS") {
		root, p, ok := strings.Cut(f, "=")
		root, p = strings.TrimSpace(root), strings.TrimSpace(p)
		if !ok || root == "" || p == "" {
			continue
		}
		if underDir(dir, root) {
			keep[p] = true
		} else if !keep[p] {
			plugins[p] = false
		}
	}
	for p := range keep {
		delete(plugins, p)
	}
	skills := map[string]string{}
	for _, s := range append(append([]string{}, leanSkillsOff...), envList("TRACKS_LEAN_SKILLS_OFF")...) {
		skills[s] = "off"
	}
	return map[string]any{
		"enabledPlugins": plugins,
		"skillOverrides": skills,
		"permissions":    map[string]any{"deny": []string{"Artifact"}},
	}
}
