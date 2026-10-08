package main

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
)

func profiles(args []string) error {
	if len(args) != 2 || args[0] != "show" {
		return errors.New("want 'lbf profiles show DIR'")
	}
	prof, err := loadProfile(args[1])
	if err != nil {
		return err
	}
	fmt.Print(describeProfile(prof, args[1]))
	return nil
}

// What 'lbf profiles show' prints: the profile, what it builds on, and what it asks of a publisher.
func describeProfile(prof *profile, ref string) string {
	var b strings.Builder
	target := prof.chosen()
	fmt.Fprintf(&b, "%s  %s\n", target.id, cmp.Or(target.title, target.id))
	if target.schema.Description != "" {
		fmt.Fprintf(&b, "  %s\n", target.schema.Description)
	}
	if bases := slices.DeleteFunc(prof.ids(), func(id string) bool { return id == target.id }); len(bases) > 0 {
		fmt.Fprintf(&b, "  Builds on %s\n", strings.Join(bases, ", "))
	}
	asks := prof.asks()
	b.WriteString("\n  Asks for:\n")
	width := 0
	for _, a := range asks {
		width = max(width, len([]rune(a.flag)))
	}
	for _, a := range asks {
		fmt.Fprintf(&b, "    %-*s  %-8s  %s\n", width, a.flag, a.need, a.hint)
	}
	fmt.Fprintf(&b, "\n  Publish with:\n    lbf publish <folder> --profile %s", ref)
	for _, a := range asks {
		if a.need == "required" && strings.HasPrefix(a.flag, "--") {
			b.WriteString(" " + a.flag)
		}
	}
	b.WriteString("\n")
	return b.String()
}
