# Documentation Language

Use ASD-STE100 Simplified Technical English Issue 9 for Workcell documents.
This rule applies to public prose, pull request text, and release notes.

## Source

The ASD Simplified Technical English Maintenance Group publishes the standard.
Request the current official copy from
[asd-ste100.org](https://www.asd-ste100.org/STE_downloads.html).

Issue 9 has 53 rules and a controlled dictionary. This page only summarizes
the rules. It does not replace the standard or its dictionary.

Keep commands, paths, identifiers, proper names, and quoted output exact. Do
not classify all these items as technical nouns or technical verbs.

## Rule summary

- Use approved dictionary words with their approved meanings.
- Use active voice.
- Use simple present, simple past, or simple future verbs.
- Use the imperative form for an instruction.
- Limit an instruction to 20 words.
- Limit a descriptive sentence to 25 words.
- Put one instruction in each sentence.
- Put one topic in each paragraph.
- Put no more than six sentences in one paragraph.
- Do not use an `-ing` verb form.
- Use an `-ing` term only as an approved word, a technical noun, or its
  modifier.
- Limit a multi-word noun to three words.
- Write an approved longer technical noun in full at its first use.
- Then use a shorter form or hyphens to show one unit.
- Use one term for one meaning.
- Use an approved technical noun or technical verb when it is necessary.

## Project terms

Use these terms with the specified meaning:

| Term | Class | Meaning |
|---|---|---|
| Workcell | proper noun | This project and product. |
| safe path | technical noun | The supported path that applies the reviewed controls. |
| runtime boundary | technical noun | The VM and container isolation boundary. |
| control plane | technical noun | The code, files, and settings that control Workcell or a provider. |
| provider state | technical noun | The provider files, cache, and session data. |
| support matrix | technical noun | The policy table that defines host support. |
| Tier 1 | technical noun | A provider support level in the provider matrix. |
| fail closed | technical verb | Stop when Workcell cannot prove a required condition. |
| stage | technical verb | Copy approved data to a controlled path before use. |
| scrub | technical verb | Remove a prohibited value from an environment or file. |
| mask | technical verb | Hide a workspace control file from the provider. |

Before you use a new project term, search this page and the applicable
contract document. Define one meaning for the term. Add only a project-specific
term that has a controlled meaning in more than one document.

## Review

Read each changed document before a commit. Compare each support claim with
`policy/host-support-matrix.tsv` and `policy/operator-contract.toml`.

Compare each requirement claim with `policy/requirements.toml`. Compare each
release claim with the published GitHub release.

Check each sentence against the full standard. Then do the public scrub and
remove private data.
