package config

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

func RenderBox(title string, sections []string, width int, alignCenter bool) string {
	if width <= 0 {
		width = 62
	}
	inner := width - 4
	horizontal := strings.Repeat("─", width-2)

	var out []string
	out = append(out, fmt.Sprintf("┌%s┐", horizontal))
	out = append(out, fmt.Sprintf("│ %s │", center(title, inner)))

	for _, section := range sections {
		out = append(out, fmt.Sprintf("├%s┤", horizontal))
		for _, text := range wrap(section, inner) {
			if alignCenter {
				out = append(out, fmt.Sprintf("│ %s │", center(text, inner)))
			} else {
				out = append(out, fmt.Sprintf("│ %s │", pad(text, inner)))
			}
		}
	}

	out = append(out, fmt.Sprintf("└%s┘", horizontal))
	return strings.Join(out, "\n")
}

func wrap(text string, width int) []string {
	var lines []string
	for _, rawLine := range strings.Split(text, "\n") {
		current := ""
		for _, word := range strings.Split(rawLine, " ") {
			if current != "" && runeLen(current)+1+runeLen(word) > width {
				lines = append(lines, current)
				current = ""
			}
			if current != "" {
				current = current + " " + word
			} else {
				current = word
			}

			for runeLen(current) > width {
				runes := []rune(current)
				lines = append(lines, string(runes[:width]))
				current = string(runes[width:])
			}
		}
		lines = append(lines, current)
	}
	return lines
}

func runeLen(s string) int {
	return utf8.RuneCountInString(s)
}

func pad(text string, width int) string {
	l := runeLen(text)
	if l >= width {
		return text
	}
	return text + strings.Repeat(" ", width-l)
}

func center(text string, width int) string {
	l := runeLen(text)
	if l >= width {
		return text
	}
	left := (width - l) / 2
	right := width - l - left
	return strings.Repeat(" ", left) + text + strings.Repeat(" ", right)
}
