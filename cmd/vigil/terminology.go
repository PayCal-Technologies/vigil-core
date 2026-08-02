package main

import "strings"

var beginnerTerminology = []struct {
	internal string
	plain    string
}{
	{"digest-bound plan", "reviewed plan that cannot change after review"},
	{"repository-mutation boundary", "files the command is allowed to change"},
	{"mutation boundary", "files the command is allowed to change"},
	{"execution envelope", "structured result"},
	{"repository fingerprint", "project snapshot used to detect changes"},
	{"capability policy", "rules for what commands can access"},
	{"capability", "what the command can access"},
	{"capabilities", "what commands can access"},
	{"artifacts", "saved reports or results"},
	{"artifact", "saved report or result"},
	{"mutation", "file change"},
	{"mutating", "file-changing"},
	{"gates", "checks"},
	{"gate", "check"},
	{"packs", "built-in feature collections"},
	{"pack", "built-in feature collection"},
	{"plugins", "installed Vigil extensions"},
	{"plugin", "installed Vigil extension"},
	{"fail closed", "stop rather than continue unsafely"},
}

func plainTerminology(value string) string {
	out := value
	for _, term := range beginnerTerminology {
		out = replaceTerm(out, term.internal, term.plain)
	}
	return out
}

func replaceTerm(value, old, new string) string {
	value = strings.ReplaceAll(value, old, new)
	if len(old) == 0 {
		return value
	}
	titleOld := strings.ToUpper(old[:1]) + old[1:]
	titleNew := strings.ToUpper(new[:1]) + new[1:]
	return strings.ReplaceAll(value, titleOld, titleNew)
}
