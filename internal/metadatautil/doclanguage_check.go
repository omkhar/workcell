// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// CheckDocLanguage enforces the four countable ASD-STE100 prose rules that
// AGENTS.md and docs/documentation-language.md already declare mandatory:
// a paragraph holds no more than six sentences, an instruction holds no more
// than twenty words, a descriptive sentence holds no more than twenty-five
// words, an -ing verb form is prohibited, and a multi-word noun holds no more
// than three words.
//
// The check is a ratchet, not a big bang. policy/doc-language-baseline.tsv
// records the count that each document carries today for each rule. A count
// above its baseline fails. A baseline row for a document that no longer
// exists also fails, so the seed list cannot outlive its subject.
func CheckDocLanguage(rootDir string) error {
	files, err := docLanguageFiles(rootDir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("document inventory under %s is empty; refusing a vacuous pass", rootDir)
	}
	baseline, err := loadDocLanguageBaseline(filepath.Join(rootDir, docLanguageBaselinePath))
	if err != nil {
		return err
	}
	counts := map[docLanguageKey]int{}
	details := map[docLanguageKey][]string{}
	for _, rel := range files {
		findings, err := scanDocLanguage(filepath.Join(rootDir, rel))
		if err != nil {
			return err
		}
		for _, finding := range findings {
			key := docLanguageKey{path: rel, rule: finding.rule}
			counts[key]++
			details[key] = append(details[key], fmt.Sprintf("%s:%d: %s: %s", rel, finding.line, finding.rule, finding.detail))
		}
	}
	var failures []string
	for key, count := range counts {
		allowed := baseline[key]
		if count <= allowed {
			continue
		}
		reported := details[key]
		if len(reported) > docLanguageMaxReported {
			reported = reported[:docLanguageMaxReported]
		}
		failures = append(failures, fmt.Sprintf("%s: %s: %d occurrence(s), baseline allows %d\n    %s",
			key.path, key.rule, count, allowed, strings.Join(reported, "\n    ")))
	}
	for key := range baseline {
		if _, ok := counts[key]; ok {
			continue
		}
		failures = append(failures, fmt.Sprintf("%s: %s: stale baseline row; the document no longer carries this rule violation, so remove the row",
			key.path, key.rule))
	}
	if len(failures) == 0 {
		return nil
	}
	sort.Strings(failures)
	return fmt.Errorf("documentation language rules failed:\n  %s", strings.Join(failures, "\n  "))
}

const (
	docLanguageBaselinePath = "policy/doc-language-baseline.tsv"
	docLanguageMaxReported  = 5

	// codeSpanPlaceholder stands in for an inline code span. It is capitalized
	// so that a code span which opens a sentence still ends the sentence before
	// it, and the sentence rules read the prose around the span.
	codeSpanPlaceholder = "Code"

	ruleParagraphSentences = "paragraph-sentences"
	ruleSentenceWords      = "sentence-words"
	ruleIngVerb            = "ing-verb"
	ruleNounCluster        = "noun-cluster"

	maxSentencesPerParagraph = 6
	maxInstructionWords      = 20
	maxDescriptiveWords      = 25
	maxNounClusterWords      = 3
)

type docLanguageKey struct {
	path string
	rule string
}

type docLanguageFinding struct {
	rule   string
	line   int
	detail string
}

// docLanguageFiles lists the public prose that the rules govern: every tracked
// Markdown page under docs/ plus the Markdown pages at the repository root.
// testdata/ holds simulated external content, not documentation.
func docLanguageFiles(rootDir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(filepath.Join(rootDir, "docs"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".md") {
			return nil
		}
		rel, relErr := filepath.Rel(rootDir, path)
		if relErr != nil {
			return relErr
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan docs/ for Markdown: %w", err)
	}
	rootFiles, err := filepath.Glob(filepath.Join(rootDir, "*.md"))
	if err != nil {
		return nil, err
	}
	for _, path := range rootFiles {
		files = append(files, filepath.Base(path))
	}
	sort.Strings(files)
	return files, nil
}

func loadDocLanguageBaseline(path string) (map[docLanguageKey]int, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", docLanguageBaselinePath, err)
	}
	baseline := map[docLanguageKey]int{}
	for number, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			return nil, fmt.Errorf("%s:%d: expected three tab-separated fields, found %d", docLanguageBaselinePath, number+1, len(fields))
		}
		count, convErr := strconv.Atoi(fields[2])
		if convErr != nil || count <= 0 {
			return nil, fmt.Errorf("%s:%d: count must be a positive integer, found %q", docLanguageBaselinePath, number+1, fields[2])
		}
		baseline[docLanguageKey{path: fields[0], rule: fields[1]}] = count
	}
	return baseline, nil
}

var (
	docLanguageCodeSpan = regexp.MustCompile("`[^`]*`")
	docLanguageLink     = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	docLanguageEmphasis = regexp.MustCompile(`\*\*|__|\*|_`)
	docLanguageListItem = regexp.MustCompile(`^([-*+]|[0-9]+[.)])\s+`)
	docLanguageFence    = regexp.MustCompile("^(```|~~~)")
	docLanguageWord     = regexp.MustCompile(`^[A-Za-z][A-Za-z-]*$`)
)

// scanDocLanguage reports one finding per rule violation in a document.
func scanDocLanguage(path string) ([]docLanguageFinding, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close() //nolint:errcheck // read-only handle

	var findings []docLanguageFinding
	var buffer []string
	start := 0
	fence := ""
	flush := func() {
		if len(buffer) == 0 {
			return
		}
		findings = append(findings, checkParagraph(strings.Join(buffer, " "), start)...)
		buffer = nil
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	number := 0
	for scanner.Scan() {
		number++
		text := strings.TrimSpace(scanner.Text())
		if marker := docLanguageFence.FindString(text); marker != "" {
			if fence == "" {
				fence = marker
			} else if strings.HasPrefix(text, fence) {
				fence = ""
			}
			flush()
			continue
		}
		if fence != "" {
			continue
		}
		// Headings, tables, block quotes, horizontal rules and inline HTML are
		// not prose paragraphs; the same exclusion set scripts/check-doc-links.sh
		// applies so example markup is never read as documentation.
		if text == "" || strings.HasPrefix(text, "#") || strings.HasPrefix(text, "|") ||
			strings.HasPrefix(text, ">") || strings.HasPrefix(text, "<") ||
			strings.HasPrefix(text, "---") || strings.HasPrefix(text, "***") {
			flush()
			continue
		}
		if item := docLanguageListItem.FindString(text); item != "" {
			// A list item is its own block: it carries one instruction, and
			// joining sibling items would invent a paragraph the author never
			// wrote.
			flush()
			start = number
			buffer = []string{strings.TrimPrefix(text, item)}
			continue
		}
		if len(buffer) == 0 {
			start = number
		}
		buffer = append(buffer, text)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	flush()
	return findings, nil
}

func checkParagraph(raw string, line int) []docLanguageFinding {
	text := docLanguageEmphasis.ReplaceAllString(
		docLanguageLink.ReplaceAllString(
			docLanguageCodeSpan.ReplaceAllString(raw, codeSpanPlaceholder), "$1"), "")
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	var findings []docLanguageFinding
	sentences := splitSentences(text)
	if len(sentences) > maxSentencesPerParagraph {
		findings = append(findings, docLanguageFinding{
			rule:   ruleParagraphSentences,
			line:   line,
			detail: fmt.Sprintf("%d sentences, limit %d", len(sentences), maxSentencesPerParagraph),
		})
	}
	for _, sentence := range sentences {
		words := sentenceWords(sentence)
		limit := maxDescriptiveWords
		kind := "descriptive sentence"
		if isInstruction(words) {
			limit = maxInstructionWords
			kind = "instruction"
		}
		if len(words) > limit {
			findings = append(findings, docLanguageFinding{
				rule:   ruleSentenceWords,
				line:   line,
				detail: fmt.Sprintf("%s of %d words, limit %d: %s", kind, len(words), limit, elide(sentence)),
			})
		}
		findings = append(findings, checkIngVerbs(words, line)...)
		findings = append(findings, checkNounClusters(words, line)...)
	}
	return findings
}

// splitSentences ends a sentence at a full stop, question mark or exclamation
// mark that a space and a capital letter follow. A version number, a file
// extension and an abbreviation keep a lowercase or digit successor, so none of
// them ends a sentence here.
func splitSentences(text string) []string {
	var sentences []string
	current := strings.Builder{}
	runes := []rune(text)
	for index, symbol := range runes {
		current.WriteRune(symbol)
		if symbol != '.' && symbol != '!' && symbol != '?' {
			continue
		}
		if index+2 >= len(runes) {
			continue
		}
		if runes[index+1] != ' ' {
			continue
		}
		next := runes[index+2]
		if next < 'A' || next > 'Z' {
			continue
		}
		sentences = append(sentences, strings.TrimSpace(current.String()))
		current.Reset()
	}
	if trimmed := strings.TrimSpace(current.String()); trimmed != "" {
		sentences = append(sentences, trimmed)
	}
	return sentences
}

// sentenceWords counts a hyphenated token as one word, the way the reviewer
// counts it, and drops bare punctuation so a dash between clauses is not a word.
func sentenceWords(sentence string) []string {
	var words []string
	for _, field := range strings.Fields(sentence) {
		trimmed := strings.Trim(field, `.,;:!?()"'“”‘’`)
		if trimmed == "" {
			continue
		}
		if !strings.ContainsAny(trimmed, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") {
			continue
		}
		words = append(words, trimmed)
	}
	return words
}

// isInstruction reports a sentence written in the imperative form, or one that
// carries an obligation modal. Both shapes take the twenty-word limit.
func isInstruction(words []string) bool {
	if len(words) == 0 {
		return false
	}
	if steImperativeVerbs[strings.ToLower(words[0])] {
		return true
	}
	for _, word := range words {
		switch strings.ToLower(word) {
		case "must", "shall":
			return true
		}
	}
	return false
}

func checkIngVerbs(words []string, line int) []docLanguageFinding {
	var findings []docLanguageFinding
	for index := 1; index < len(words); index++ {
		word := strings.ToLower(words[index])
		if !docLanguageWord.MatchString(words[index]) || !strings.HasSuffix(word, "ing") || len(word) <= 5 {
			continue
		}
		if steApprovedIngTerms[word] {
			continue
		}
		// An -ing token that a preposition or a subordinator introduces is a
		// verb form, not a technical noun. A token in any other position is
		// left alone: telling "staging path" from "staging the path" needs a
		// part-of-speech tagger, and this check does not run one.
		if !steIngIntroducers[strings.ToLower(words[index-1])] {
			continue
		}
		findings = append(findings, docLanguageFinding{
			rule:   ruleIngVerb,
			line:   line,
			detail: fmt.Sprintf("prohibited -ing verb form %q after %q", words[index], words[index-1]),
		})
	}
	return findings
}

// checkNounClusters reports a noun cluster longer than three words.
//
// ponytail: heuristic, not a parser. A cluster is a run of lowercase alphabetic
// tokens that a determiner introduces and that a non-noun token or the end of
// the sentence closes, with the function words and the known verbs of
// steClusterBreakers removed from the run. A part-of-speech tagger would decide
// this exactly; it would also add a model dependency to a docs lane that has
// none. Raise the ceiling by replacing this function with a tagger when the
// false-positive rate stops being acceptable, and widen steClusterBreakers
// until then.
func checkNounClusters(words []string, line int) []docLanguageFinding {
	var findings []docLanguageFinding
	run := 0
	introduced := false
	for index := 0; index <= len(words); index++ {
		token := ""
		if index < len(words) {
			token = strings.ToLower(words[index])
		}
		isNoun := index < len(words) &&
			docLanguageWord.MatchString(words[index]) &&
			words[index] == token &&
			!strings.Contains(token, "-") &&
			!steClusterBreakers[token] &&
			!steImperativeVerbs[token] &&
			!steImperativeVerbs[strings.TrimSuffix(token, "s")] &&
			!strings.HasSuffix(token, "ly") &&
			!strings.HasSuffix(token, "s") &&
			(!strings.HasSuffix(token, "ing") || steApprovedIngTerms[token])
		if isNoun {
			if run == 0 {
				introduced = index > 0 && steDeterminers[strings.ToLower(words[index-1])]
			}
			run++
			continue
		}
		if introduced && run > maxNounClusterWords {
			cluster := strings.Join(words[index-run:index], " ")
			findings = append(findings, docLanguageFinding{
				rule:   ruleNounCluster,
				line:   line,
				detail: fmt.Sprintf("%d-word noun cluster %q, limit %d", run, cluster, maxNounClusterWords),
			})
		}
		run = 0
		introduced = false
	}
	return findings
}

func elide(sentence string) string {
	if len(sentence) <= 80 {
		return sentence
	}
	return sentence[:77] + "..."
}

func stringSet(words ...string) map[string]bool {
	set := make(map[string]bool, len(words))
	for _, word := range words {
		set[word] = true
	}
	return set
}

// steImperativeVerbs lists the sentence-initial verbs that the Workcell
// documents use for an instruction. STE requires the imperative form for an
// instruction, so the first word identifies one.
var steImperativeVerbs = stringSet(
	"add", "allow", "apply", "assert", "avoid", "block", "build", "call", "capture",
	"check", "choose", "clone", "compare", "complete", "confirm", "copy", "count",
	"create", "define", "delete", "describe", "do", "document", "enable", "ensure",
	"enter", "exit", "expect", "export", "extract", "fail", "fetch", "fix", "follow",
	"generate", "give", "grant", "hold", "include", "install", "keep", "limit", "list",
	"make", "mask", "measure", "merge", "move", "name", "open", "pass", "pin", "prefer",
	"prepare", "preserve", "prove", "publish", "pull", "push", "put", "read", "rebuild",
	"record", "reject", "remove", "rename", "repeat", "replace", "report", "request",
	"require", "rerun", "reset", "resolve", "restore", "return", "review", "rotate",
	"run", "scrub", "see", "select", "send", "set", "sign", "skip", "split", "stage",
	"start", "state", "stop", "store", "tag", "take", "test", "track", "treat", "trust",
	"update", "upload", "use", "verify", "wait", "write",
)

// steApprovedIngTerms lists the -ing tokens that Workcell documents use as an
// approved technical noun or its modifier, which STE permits.
var steApprovedIngTerms = stringSet(
	"during", "logging", "packaging", "signing", "staging", "tooling", "warning", "warnings",
)

// steIngIntroducers lists the prepositions, subordinators and auxiliaries that
// put an -ing token in verb position.
var steIngIntroducers = stringSet(
	"after", "am", "are", "avoid", "be", "been", "before", "being", "by", "for",
	"is", "of", "start", "stop", "was", "were", "while", "with", "without",
)

// steDeterminers lists the words that open a noun phrase.
var steDeterminers = stringSet(
	"a", "an", "another", "any", "each", "every", "her", "his", "its", "no", "one",
	"our", "some", "that", "the", "their", "these", "this", "those", "your",
)

// steClusterBreakers lists the function words and common verbs that close a
// noun cluster. It is deliberately wide: a missing entry produces a false
// positive, and a surplus entry only produces a missed one.
var steClusterBreakers = stringSet(
	"a", "about", "above", "across", "after", "against", "all", "also", "an", "and",
	"any", "are", "as", "at", "be", "because", "been", "before", "below", "both",
	"but", "by", "can", "cannot", "code", "do", "does", "each", "either", "else",
	"every", "for", "from", "has", "have", "how", "if", "in", "into", "is", "it",
	"its", "may", "must", "no", "nor", "not", "of", "on", "once", "one", "only",
	"or", "other", "out", "over", "per", "shall", "should", "since", "so", "still",
	"such", "than", "that", "the", "their", "them", "then", "there", "these", "they",
	"this", "those", "through", "to", "under", "until", "up", "use", "uses", "via",
	"was", "were", "when", "where", "which", "while", "who", "whose", "why", "will",
	"with", "within", "without", "you", "your",
)
