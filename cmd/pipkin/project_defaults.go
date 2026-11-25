package main

import (
	"embed"
	"fmt"
	"os"
	"slices"
	"strings"
)

//go:embed skills/*/SKILL.md
var bundledSkills embed.FS

type projectDefaults struct {
	skill string
	agent AgentProfile
}

var defaultsByProject = map[string]projectDefaults{
	"general": {"", AgentProfile{
		ReviewPasses: 1, ReviewContextBytes: 4096,
		DraftFocus:  "Identify the deliverable and its acceptance checks. Complete it in the requested format.",
		ReviewFocus: "Check completeness, usability, and unsupported assumptions.",
	}},
	"software": {"software-maintenance", AgentProfile{
		ReviewPasses: 2, ReviewContextBytes: 8192,
		DraftFocus:        "Trace the normal entry point and preserve existing callers and data.",
		ReviewFocus:       "Check behavioral regressions, compatibility, retries, and error paths.",
		VerificationFocus: "Check whether the proposed targeted tests exercise the changed behavior and catch the identified regression.",
	}},
	"software-new": {"software-bootstrap", AgentProfile{
		ReviewPasses: 2, ReviewContextBytes: 4096,
		DraftFocus:        "Build a minimal runnable milestone with a smoke check.",
		ReviewFocus:       "Reason through a clean checkout, setup, normal run, and failure path.",
		VerificationFocus: "Check that dependency, configuration, and run instructions are complete and agree with the supplied implementation.",
	}},
	"research": {"research-evidence", AgentProfile{
		ReviewPasses: 2, ReviewContextBytes: 8192,
		DraftFocus:        "Structure findings around the question and include an evidence trail.",
		ReviewFocus:       "Audit each material claim against its cited source and check calculations.",
		VerificationFocus: "Check evidence provenance, uncertainty, conflicting findings, and scope of conclusions.",
	}},
	"pitch": {"pitch-package", AgentProfile{
		ReviewPasses: 2, ReviewContextBytes: 4096,
		DraftFocus:        "Write for the recipient's decision and make the ask explicit and supportable.",
		ReviewFocus:       "Check substantiation, consistent figures, format, and submission requirements.",
		VerificationFocus: "Read as the intended recipient and check that the decision, evidence, and next action are clear.",
	}},
	"job-search": {"job-search", AgentProfile{
		ReviewPasses: 2, ReviewContextBytes: 4096,
		DraftFocus:        "Apply hard filters first and use verified experience to explain fit.",
		ReviewFocus:       "Check eligibility, exclusions, dates, role status, and claims in application material.",
		VerificationFocus: "Check that uncertain details stay marked as unknown and every tailored claim matches verified experience.",
	}},
}

func projectSkillGuidance(id string) ([]profileGuidance, error) {
	defaults, ok := defaultsByProject[id]
	if !ok {
		return nil, fmt.Errorf("missing defaults for project profile %q", id)
	}
	names := []string{"deliverable-review"}
	if defaults.skill != "" {
		names = append(names, defaults.skill)
	}
	return readBundledSkills(names)
}

func readBundledSkills(names []string) ([]profileGuidance, error) {
	var guidance []profileGuidance
	for _, name := range names {
		data, err := bundledSkills.ReadFile("skills/" + name + "/SKILL.md")
		if err != nil {
			return nil, err
		}
		_, body, _ := strings.Cut(string(data), "\n---\n")
		guidance = append(guidance, profileGuidance{"Bundled skill: " + name, body})
	}
	return guidance, nil
}

type bundledSkillView struct {
	Name, Body, Path string
	Active           bool
}

func bundledSkillCatalog() []bundledSkillView {
	dirs, _ := bundledSkills.ReadDir("skills")
	var catalog []bundledSkillView
	for _, dir := range dirs {
		guidance, err := readBundledSkills([]string{dir.Name()})
		if err == nil {
			catalog = append(catalog, bundledSkillView{Name: dir.Name(), Body: guidance[0].prompt, Path: bundledSkillPath(dir.Name())})
		}
	}
	return catalog
}

func bundledSkillPath(name string) string {
	return skillsDirectory + "/" + name + "/SKILL.md"
}

func bundledSkillTemplate(name string) ([]byte, error) {
	return bundledSkills.ReadFile("skills/" + name + "/SKILL.md")
}

func workspaceBundledSkills(root string, settings instructionSettings, tools []string) []bundledSkillView {
	catalog := bundledSkillCatalog()
	defaults := make(map[string]bool)
	if !settings.SkipProjectInstructions {
		if project, err := readProject(root); err == nil {
			defaults["deliverable-review"] = true
			defaults[defaultsByProject[project.Profile].skill] = true
		}
	}
	if guidance, err := toolSkillGuidance(tools); err == nil {
		for _, skill := range guidance {
			defaults[strings.TrimPrefix(skill.label, "Bundled skill: ")] = true
		}
	}
	for i := range catalog {
		item := &catalog[i]
		item.Active = defaults[item.Name] || slices.Contains(settings.Files, item.Path)
		if active, found := settings.BundledSkills[item.Name]; found {
			item.Active = active
		}
	}
	return catalog
}

func editableWorkspaceSkills(root string) []string {
	var paths []string
	for _, path := range workspaceSkills(root) {
		name, _ := standardInstructionPath(path)
		if _, err := bundledSkillTemplate(name); err != nil {
			paths = append(paths, path)
		}
	}
	return paths
}

func toolSkillGuidance(tools []string) ([]profileGuidance, error) {
	var names []string
	for _, tool := range tools {
		switch tool {
		case "web_search":
			names = append(names, "web-research", "public-web-extraction")
		case "code_interpreter":
			names = append(names, "python-authoring", "python-execution")
		}
	}
	return readBundledSkills(names)
}

// Explicit agent selections keep their existing behavior. Project routing is
// used for agentic deliveries when no explicit selection is set.
func (c Config) deliveryAgentProfile(root string) (string, AgentProfile, error) {
	name := strings.TrimSpace(os.Getenv("PIPKIN_AGENT_PROFILE"))
	if name == "" {
		name = c.AgentProfile
	}
	if name != "" && name != "project" {
		return c.activeAgentProfile()
	}
	project, err := readProject(root)
	if err != nil {
		return "", AgentProfile{}, err
	}
	name = "project:" + project.Profile
	if _, exists := c.AgentProfiles[name]; exists {
		profile, err := c.agentProfileByName(name)
		return name, profile, err
	}
	profile := defaultsByProject[project.Profile].agent
	profile.Runner = "builtin"
	if c.LangGraphPython != "" {
		profile.Runner = "langgraph"
	}
	return name, profile, nil
}
