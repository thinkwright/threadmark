package main

import (
	"regexp"
	"strings"
)

func argvHasFlagValue(args []string, name, value string) bool {
	for idx := 0; idx < len(args)-1; idx++ {
		if args[idx] == name && args[idx+1] == value {
			return true
		}
	}
	prefix := name + "="
	for _, arg := range args {
		if strings.TrimPrefix(arg, prefix) != arg && strings.TrimPrefix(arg, prefix) == value {
			return true
		}
	}
	return false
}

func commandLineHasFlagValue(commandLine, name, value string) bool {
	boundary := `(?:^|\s)`
	end := `(?:\s|$)`
	separate := regexp.MustCompile(boundary + regexp.QuoteMeta(name) + `\s+` + regexp.QuoteMeta(value) + end)
	if separate.MatchString(commandLine) {
		return true
	}
	equals := regexp.MustCompile(boundary + regexp.QuoteMeta(name+"="+value) + end)
	return equals.MatchString(commandLine)
}
