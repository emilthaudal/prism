package tracks

import (
	"os"
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

// leanSettings are the settings keys that cut a track session's start-up context: skills and
// plugins it does not use (TRACKS_LEAN_PLUGINS_OFF lists plugins, comma-separated, as
// name@marketplace), and the Artifact tool. They apply to the session's process, so its
// subagents get them too.
func leanSettings() map[string]any {
	plugins := map[string]bool{}
	for _, p := range envList("TRACKS_LEAN_PLUGINS_OFF") {
		plugins[p] = false
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
