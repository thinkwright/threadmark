package main

import "strings"

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
