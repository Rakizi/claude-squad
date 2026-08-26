package overlay

import (
	"strings"
	"unicode"
)

// PickByRune resolves a keypress to an index in a list of labels, or -1.
//
// ⛔ WHY THIS EXISTS, measured 2026-08-26 by an agent driving the interface
// through tmux: STEPPING IS WHERE EVERY ERROR HAPPENED. Arrow keys have
// intermediate states, and every intermediate state has to be READ correctly to
// know when to stop. The agent read the wrong marker and moved to the wrong row;
// read an empty capture and kept pressing anyway; overshot the profile it wanted
// and landed one past it. None of those are possible when the choice is one key.
//
// It helps the human for the ordinary reason -- nobody enjoys pressing Right
// four times -- and it helps an agent far more, because a direct key has nothing
// to observe between intent and result.
//
// TWO LAYERS, deliberately:
//
//	1-9     POSITION. Unambiguous and stable. An agent reads the list and knows
//	        the index without knowing any collision rule.
//	letter  MNEMONIC. What a human remembers. Matched on the label's first
//	        SIGNIFICANT rune, and when several share it, pressing again CYCLES
//	        through them -- the behaviour every desktop menu has had for
//	        decades, so it needs no explaining.
//
// ⚠ `from` is the CURRENT index and matters only for cycling: with three labels
// starting "n", pressing n repeatedly walks n1 -> n2 -> n3 -> n1. Passing a
// nonsense `from` still returns a correct match, just possibly not the next one.
func PickByRune(labels []string, r rune, from int) int {
	if len(labels) == 0 {
		return -1
	}
	// Digits address position. 1 is the first, not the zeroth: the list is
	// PRINTED "1." and a shortcut that disagrees with the thing on screen is
	// worse than no shortcut.
	if r >= '1' && r <= '9' {
		if i := int(r - '1'); i < len(labels) {
			return i
		}
		return -1
	}
	target := unicode.ToLower(r)
	if !unicode.IsLetter(target) {
		return -1
	}
	// Start looking AFTER the current position so a repeated press advances.
	n := len(labels)
	start := from + 1
	if from < 0 || from >= n {
		start = 0
	}
	for k := 0; k < n; k++ {
		i := (start + k) % n
		if pickRune(labels[i]) == target {
			return i
		}
	}
	return -1
}

// pickRune is the letter a label answers to: its first letter, ignoring any
// leading path or punctuation.
//
// ⚠ A repo label is a PATH -- "the-lab/NextActionGuide". Its first character is
// "t", which would make every repo answer to the same key and make the whole
// feature useless. The part a human reads is the last segment, so that is what
// it matches on.
func pickRune(label string) rune {
	s := label
	if i := strings.LastIndex(s, "/"); i >= 0 && i+1 < len(s) {
		s = s[i+1:]
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
	}
	return 0
}
