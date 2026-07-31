package main

import "testing"

func TestActiveCommandsIncludePublicCICD(t *testing.T) {
	commands := map[string]bool{}
	for _, command := range activeCommands() {
		commands[command.Command] = true
	}
	for _, want := range []string{
		"doctor",
		"status",
		"plan",
		"workflow:local",
		"hooks:install",
		"hooks:pre-commit",
		"hooks:pre-push",
		"checks:public-assumptions",
		"checks:command-catalog",
		"files:iterate",
		"readme:generate",
		"readme:check",
		"a11y:inventory",
		"checks:dependency-security",
		"checks:release-policy",
		"checks:tracked-assistant-artifacts",
		"security:gitleaks",
		"repo:health",
		"config:report",
		"config:template",
		"explain",
		"guards:summary",
		"self-heal:plan",
		"next",
		"tools:catalog",
		"resources:catalog",
		"deploy:verify",
		"tests:history",
		"tests:affected",
		"javascript:quality",
		"php:lint",
		"phpstan:analyse",
	} {
		if !commands[want] {
			t.Fatalf("active command %s missing", want)
		}
	}
}

func TestPublicAssumptionScanIsClean(t *testing.T) {
	findings, err := publicAssumptionFindings("")
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("public assumption findings: %#v", findings)
	}
}

func TestConfigTemplateValidates(t *testing.T) {
	for _, profile := range []string{"generic", "go-tool", "static-site"} {
		if err := validateStruct(templateConfig(profile)); err != nil {
			t.Fatalf("templateConfig(%s) failed validation: %v", profile, err)
		}
	}
}

func TestValidateConfigIssuesExplainsMissingFields(t *testing.T) {
	issues := validateConfigIssues(config{})
	if len(issues) == 0 {
		t.Fatal("expected missing config issues")
	}
	fields := map[string]bool{}
	for _, issue := range issues {
		fields[issue.Field] = true
		if issue.Code == "" || issue.Message == "" {
			t.Fatalf("issue missing code or message: %#v", issue)
		}
	}
	for _, want := range []string{"schema_version", "profile", "project", "authority.mutation_requires", "gates"} {
		if !fields[want] {
			t.Fatalf("expected issue for %s, got %#v", want, issues)
		}
	}
}

func TestApplyConfigDefaultsRepairsMinimalConfig(t *testing.T) {
	cfg := applyConfigDefaults(config{}, "generic")
	if err := validateStruct(cfg); err != nil {
		t.Fatalf("default repair failed validation: %v", err)
	}
}

func TestApplyConfigDefaultsDropsInvalidPublicAssumptionPatterns(t *testing.T) {
	cfg := applyConfigDefaults(config{PublicAssumptionPatterns: []string{"(?i)sample-pattern", "["}}, "generic")
	if err := validateStruct(cfg); err != nil {
		t.Fatalf("default repair failed validation: %v", err)
	}
	if len(cfg.PublicAssumptionPatterns) != 1 || cfg.PublicAssumptionPatterns[0] != "(?i)sample-pattern" {
		t.Fatalf("unexpected repaired patterns: %#v", cfg.PublicAssumptionPatterns)
	}
}

func TestScribeReadmeRenderIsStable(t *testing.T) {
	input := "# App\n\nHuman intro.\n\n## Install\n"
	next, changed := renderScribeReadme(input)
	if !changed {
		t.Fatal("expected Scribe render to add managed block")
	}
	again, changed := renderScribeReadme(next)
	if changed {
		t.Fatalf("expected Scribe render to be stable, changed to:\n%s", again)
	}
	if next != again {
		t.Fatal("stable Scribe render changed content")
	}
}

func TestUniqueStringsPreservesOrder(t *testing.T) {
	got := uniqueStrings([]string{"a", "b", "a", "", "c"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("uniqueStrings length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("uniqueStrings[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}
