# Architecture and per-module documentation: primary-source research

Date: 2026-09-03

Status: primary-source survey (general practice, not scoped to this repo)

Scope: this document surveys what the owning sources for each documentation practice actually claim, with a citation for every claim. It does not recommend how loomcli specifically should apply any of it — that mapping is left to a separate pass.

## What the evidence actually supports

Across independent primary sources (C4, arc42, Nygard/MADR, Diátaxis, Go, Google, Write the Docs, Anthropic), the same handful of practices recur. These are the load-bearing conclusions; everything else in this document is detail and citation.

1. **Prefer generated or enforced documentation over hand-written prose wherever the information can be derived from the artifact itself.** C4 explicitly says its lowest (Code) level "is often available on-demand from tooling" and should "ideally... be automatically generated"; the Component level should be automated "for long-lived documentation" ([c4model.com/diagrams/code](https://c4model.com/diagrams/code), [c4model.com/diagrams/component](https://c4model.com/diagrams/component)). Structurizr's whole pitch is diagrams generated from one model instead of hand-drawn and drifting ([structurizr.com](https://structurizr.com)). ArchUnit and dependency-cruiser convert what would be prose module-boundary rules into executable, CI-enforced tests ([archunit.org](https://www.archunit.org/), [github.com/sverweij/dependency-cruiser](https://github.com/sverweij/dependency-cruiser)). Go's `internal/` mechanism enforces a boundary via the toolchain rather than a comment asking people not to import something ([go.dev/doc/go1.4#internalpackages](https://go.dev/doc/go1.4#internalpackages)).
2. **Match the diagram/document to a specific, named audience and stop there — don't chase completeness.** Every C4 level statement pairs the diagram with an explicit audience and an explicit "detail isn't important here" boundary ([c4model.com/diagrams/system-context](https://c4model.com/diagrams/system-context), [container](https://c4model.com/diagrams/container), [component](https://c4model.com/diagrams/component)). Google's docguide states the same principle for prose: "Brief and utilitarian is better than long and exhaustive... users need only a small fraction of the author's total knowledge, but they need it quickly" ([google.github.io/styleguide/docguide/philosophy.html](https://google.github.io/styleguide/docguide/philosophy.html)).
3. **Record decisions, not designs, in small immutable-once-accepted units — one significant decision per record.** This is the common thread across Nygard's original post, adr.github.io, MADR, and AWS: an ADR is "a single AD," accepted ADRs are immutable and superseded rather than edited ([cognitect.com/blog/2011/11/15/documenting-architecture-decisions](https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions), [adr.github.io](https://adr.github.io/), [docs.aws.amazon.com/.../best-practices.html](https://docs.aws.amazon.com/prescriptive-guidance/latest/architectural-decision-records/best-practices.html)).
4. **Keep documentation next to the code, under the same version control and review workflow, changed in the same commit/CL as the code.** This is Write the Docs' definition of docs-as-code itself ([writethedocs.org/guide/docs-as-code](https://www.writethedocs.org/guide/docs-as-code/)), GitLab's practice ([docs.gitlab.com/development/documentation](https://docs.gitlab.com/development/documentation/)), and Google's explicit instruction: "Change your documentation in the same CL as the code change" ([google.github.io/styleguide/docguide/best_practices.html](https://google.github.io/styleguide/docguide/best_practices.html)).
5. **Don't mix documentation modes in one document — each mode (tutorial/how-to/reference/explanation) has a different reader need, and blending them actively harms all of them.** Diátaxis states this as a named failure mode, not a stylistic preference: "How-to guides are wholly distinct from tutorials. They are often confused... Conflating them is at the root of many difficulties that afflict documentation" ([diataxis.fr/how-to-guides](https://diataxis.fr/how-to-guides/)); the same warning is made for explanation content absorbing instruction ([diataxis.fr/explanation](https://diataxis.fr/explanation/)).
6. **Stale documentation is treated as actively harmful, not merely low-value, and deletion is a legitimate maintenance action.** Google: "Dead docs are bad. They misinform, they slow down, they incite despair in engineers" ([google.github.io/styleguide/docguide/best_practices.html](https://google.github.io/styleguide/docguide/best_practices.html)).
7. **Documentation written for agents is optimized differently than documentation for humans: it must make implicit context explicit, and it is organized for progressive/on-demand disclosure rather than linear reading.** Anthropic: "think of how you would describe your tool to a new hire... make it explicit," and skill content should be split so "Claude will read [it] only when" relevant, keeping the always-loaded core lean ([anthropic.com/engineering/writing-tools-for-agents](https://www.anthropic.com/engineering/writing-tools-for-agents), [anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills](https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills)).
8. **Every framework in this survey is explicitly presented as tailorable/optional-in-parts, not as a checklist to complete in full.** arc42: "Twelve sections, each with a clear purpose, tailorable to your specific needs," plus a one-page "canvas" alternative ([arc42.org/overview](https://arc42.org/overview)); C4: "notation independent, and doesn't prescribe any particular notation" ([c4model.com/diagrams/notation](https://c4model.com/diagrams/notation)).

## 1. C4 model

**Owner / primary source:** [c4model.com](https://c4model.com/), by Simon Brown.

**What it is, per its own description:** "The C4 model is an easy to learn, developer friendly approach to software architecture diagramming" ([c4model.com](https://c4model.com/)).

### The four levels

| Level | What it's for | Audience (stated) | Detail boundary (stated) |
|---|---|---|---|
| System Context | "your system as a box in the centre, surrounded by its users and the other systems that it interacts with... a good starting point for diagramming and documenting a software system, allowing you to step back and see the big picture" | "Everybody, both technical and non-technical people, inside and outside the software development team" — explicitly "the sort of diagram that you could show to non-technical people" | "Detail isn't important here as this is your zoomed out view showing a big picture" | 
| Container | "shows the high-level shape of the software architecture and how responsibilities are distributed across it... major technology choices and how the containers communicate" | "Technical people inside and outside the software development team; including software architects, developers and operations/support staff" | Explicitly excludes deployment topology: "says very little about deployment aspects such as clustering, load balancers, replication, failover, etc because it will likely vary across different environments. Deployment information is better captured via one or more deployment diagrams" |
| Component | "zoom in and decompose a container to describe the components that reside inside it; including their responsibilities and the technology/implementation details" | "Software architects and developers" | Not mandatory: "only create component diagrams if you feel they add value, and consider automating their creation for long-lived documentation" |
| Code | zooms into a component (class diagrams, ER diagrams, etc.) | (implementation-level; IDE/tooling audience) | Explicitly the least recommended: "very much an optional level of detail," "not recommended for anything but the most important or complex components," "Ideally this diagram would be automatically generated using tooling (e.g. an IDE or UML modelling tool)" |

Sources: [c4model.com/diagrams/system-context](https://c4model.com/diagrams/system-context), [c4model.com/diagrams/container](https://c4model.com/diagrams/container), [c4model.com/diagrams/component](https://c4model.com/diagrams/component), [c4model.com/diagrams/code](https://c4model.com/diagrams/code).

Note on "container": C4 explicitly disambiguates its own term from Docker: a container is "an application or a data store," not necessarily a Docker/OCI container ([c4model.com/diagrams/container](https://c4model.com/diagrams/container)). This contradicts a common misreading of the term.

### What C4 explicitly says not to do

- Don't over-detail the Code level — it's optional, tool-generated, and reserved for the most complex components only ([c4model.com/diagrams/code](https://c4model.com/diagrams/code)).
- Don't hand-maintain Component diagrams by default — only create them if they add value, and automate if you keep them long-term ([c4model.com/diagrams/component](https://c4model.com/diagrams/component)).
- Don't cram deployment topology into the Container diagram — that belongs in a separate deployment diagram ([c4model.com/diagrams/container](https://c4model.com/diagrams/container)).
- Don't ship a diagram without a title or a key/legend — both are stated as required, not optional, on every diagram (see Notation below).

The site does not, in the pages fetched, contain a single consolidated "anti-patterns" list; the warnings are distributed per-level as above.

### Notation guidance

- "The C4 model is notation independent, and doesn't prescribe any particular notation" — it explicitly declines to mandate UML, ArchiMate, or any specific shape/color scheme, and diagrams may legitimately use those notations if preferred ([c4model.com/diagrams/notation](https://c4model.com/diagrams/notation)).
- But it requires structure around that freedom: "Every diagram should have a title describing the diagram type and scope," and "Every diagram should have a key/legend explaining the notation being used" (shapes, colors, border styles, line types, arrow heads) ([c4model.com/diagrams/notation](https://c4model.com/diagrams/notation)).
- Every element's type must be explicit (Person / Software System / Container / Component) and carry a short description; every relationship (line) must be a labeled, unidirectional arrow, and container-to-container relationships must label the technology/protocol ([c4model.com/diagrams/notation](https://c4model.com/diagrams/notation)).
- Color is explicitly not dictated: "this isn't something that is dictated by the C4 model, and you are free to use whatever colours you like!" — with the caveat to stay consistent and consider colorblind/black-and-white-printer readers ([c4model.com/diagrams/notation](https://c4model.com/diagrams/notation)).

### Supplementary diagrams beyond the core 4

C4 explicitly has "an additional set of supporting diagrams" beyond the 4 core levels: **System Landscape**, **Dynamic**, and **Deployment** diagrams ([c4model.com](https://c4model.com/)). Deployment in particular is the designated place for the topology detail explicitly excluded from the Container diagram.

### Tooling

- **Structurizr DSL** (structurizr.com, same author as C4): "a 'models as code' tool designed for the C4 model — you write Structurizr DSL to create multiple software architecture diagrams from a single model." It is described as "the reference implementation" for C4 compliance, generating System Landscape, System Context, Container, Component, Dynamic, and Deployment views from one model, which is the direct fix for the diagram/reality-drift problem ([structurizr.com](https://structurizr.com)).
- **Mermaid's C4 support**: Mermaid documents this explicitly as unstable: "This is an experimental diagram for now. The syntax and properties can change in future releases." It supports `C4Context`, `C4Container`, `C4Component`, `C4Dynamic`, and `C4Deployment`, is stated to be syntax-compatible with PlantUML's C4-PlantUML project, and its own docs note the layout is not a true auto-layout — shape position is controlled by statement order ([mermaid.js.org/syntax/c4.html](https://mermaid.js.org/syntax/c4.html)). Treat Mermaid C4 as convenient-but-unstable, per Mermaid's own disclaimer, not as an equally authoritative tool to Structurizr.

## 2. arc42

**Owner / primary source:** [arc42.org](https://arc42.org/overview) and its documentation site [docs.arc42.org](https://docs.arc42.org/).

### The 12 sections

1. **Introduction & Goals** — fundamental requirements, especially quality goals
2. **Constraints** — regulations and external constraints
3. **Context & Scope** — external systems and interfaces
4. **Solution Strategy** — core ideas and solution approaches
5. **Building Block View** — structure of source code / modularization (arc42 itself flags this as usually the most extensive section)
6. **Runtime View** — important runtime scenarios
7. **Deployment View** — hardware, infrastructure, deployment
8. **Crosscutting Concepts** — overarching technical topics, patterns, processes
9. **Architectural Decisions** — important decisions not described elsewhere
10. **Quality Requirements** — quality tree and quality scenarios
11. **Risks & Technical Debt** — known problems and risks
12. **Glossary** — important/specific terms

Source: [arc42.org/overview](https://arc42.org/overview).

### arc42's own stance on completeness

arc42 explicitly frames the template as tailorable, not a checklist to fill exhaustively: "Twelve sections, each with a clear purpose, tailorable to your specific needs" ([arc42.org/overview](https://arc42.org/overview)). For teams wanting something lighter, arc42 itself publishes an alternative: "an arc42 canvas captures a whole system on a single page" ([arc42.org/overview](https://arc42.org/overview)). The site does not, in the page fetched, name individual sections as "skip this one" — the adaptability is stated at the template level, and the canvas is the sanctioned lightweight path.

### Quality tree / quality scenarios (section 10)

- A quality scenario's purpose, per arc42: to make "quality requirements concrete and allow to decide whether they are fulfilled (in the sense of acceptance criteria)."
- Two scenario kinds: **usage scenarios** ("describe the system's runtime reaction to a certain stimulus," including efficiency/performance) and **change scenarios** ("describe the desired effect of a modification or extension of the system").
- Scenario format, short form: Context/Background, Source/Stimulus, Metric/Acceptance Criteria. Long form adds: Scenario ID, Name, Source, Stimulus, Environment, Artifact, Response, and Response Measure ("the criteria or metric by which the system's response is evaluated").
- On the "quality tree" itself: arc42's current material leans on a newer, flatter model — the **"Q42 quality model"** — using flat labels (e.g. `#flexible`, `#efficient`, `#usable`, `#operable`, `#testable`, `#secure`, `#safe`, `#reliable`) rather than a strict hierarchical tree, while still referencing the classical "Quality Attribute Utility Tree" (root = "quality," refined into a tree) as the originating idea.

Source: [docs.arc42.org](https://docs.arc42.org/) (section 10 content as fetched).

### arc42 and ADRs — an explicit cross-reference

Section 9 (Architectural Decisions) explicitly recommends using ADRs as the format: "ADR (architecture decision record) for every important decision," and states "smaller pieces of documentation are easier to read, create and maintain." It leaves embed-vs-external-log as a judgment call ("list or table... or more detailed in form of separate sections per decision") and explicitly warns against duplicating section 4 (Solution Strategy): "Avoid redundant texts," since major decisions are "already" covered there. It also gives a placement rule: "use your judgement to decide whether an architectural decision should be documented here in this central section or whether you better document it locally (e.g. within the white box template of one building block)" ([docs.arc42.org/section-9](https://docs.arc42.org/section-9/)).

## 3. Architecture Decision Records (ADRs)

### Michael Nygard's original proposal (2011)

Primary source: [cognitect.com/blog/2011/11/15/documenting-architecture-decisions](https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions).

- **Structure**: Title ("short noun phrases"), Context (the forces at play — "technological, political, social, and project local" — described in "value-neutral" language), Decision ("stated in full sentences, with active voice. 'We will …'"), Consequences ("describes the resulting context, after applying the decision. All consequences should be listed here, not just the 'positive' ones.")
- **Status**: "proposed" if stakeholders haven't agreed, "accepted" once agreement is reached, "deprecated" or "superseded" when a later ADR reverses it (with a reference to the replacement).
- **Immutability**: "If a decision is reversed, we will keep the old one around, but mark it as superseded. (It's still relevant to know that it *was* the decision, but is *no longer* the decision.)" — the old ADR is not edited or deleted, only its status changes.
- **Rationale**: "One of the hardest things to track during the life of a project is the motivation behind certain decisions." ADRs exist so people neither "blindly accept" nor "blindly change" past decisions without the context that produced them.

### adr-tools

Primary source: [github.com/npryce/adr-tools](https://github.com/npryce/adr-tools) (Nat Pryce).

- Commands: `adr init` (initialize the ADR directory), `adr new` (create a new numbered ADR and open it in `$EDITOR`), `adr new -s <number>` (create an ADR that supersedes an existing one), `adr help`.
- Convention: ADRs are Markdown files in a project subdirectory, default `doc/adr`.
- Superseding is a first-class operation: `-s` "creates a new ADR file that is flagged as superceding ADR 9, and changes the status of ADR 9 to indicate that it is superceded by the new ADR" — i.e., the tool encodes Nygard's immutability rule directly.

### MADR (Markdown Architectural Decision Records)

Primary source: [adr.github.io/madr](https://adr.github.io/madr/).

- Current spec version at time of research: MADR 4.0.0, released 2024-09-17, with "bare" and "minimal" template variants.
- Template fields: YAML front matter (status, date, decision-makers, consulted, informed) → Context and Problem Statement → Decision Drivers (optional) → Considered Options → Decision Outcome → Consequences (optional; merges "good/bad because" reasoning) → Confirmation (optional — how compliance will be verified) → Pros and Cons of the Options (optional, detailed) → More Information (optional).
- Difference from Nygard's original: MADR is considerably more structured, explicitly enumerating *considered options* and *decision drivers* as first-class fields — Nygard's original format doesn't name these as separate sections, folding alternatives implicitly into Context/Decision. MADR 4.0 also consolidated what had been separate positive/negative consequence lists into one unified Consequences section.

### adr.github.io (umbrella site)

Primary source: [adr.github.io](https://adr.github.io/).

- Defines an "Architectural Decision" as "a justified design choice that addresses a functional or non-functional requirement that is architecturally significant," and an ADR as capturing "a single AD and its rationale."
- States the granularity rule explicitly: **one decision per record**, collected together into a project's "decision log."
- Ties "architecturally significant" to Architecturally Significant Requirements (ASRs) — requirements with "a measurable effect on the architecture and quality of a software... system."
- Credits Nygard's 2011 post with popularizing the concept, and also cites "Sustainable Architectural Decisions" (Zdun et al.) as the source of the Y-statement format offered as an alternative lightweight template.

### AWS Prescriptive Guidance

Primary source: [docs.aws.amazon.com/prescriptive-guidance/latest/architectural-decision-records](https://docs.aws.amazon.com/prescriptive-guidance/latest/architectural-decision-records/best-practices.html).

- Reaffirms immutability as policy: "When the team accepts an ADR, it becomes immutable. If new insights require a different decision, the team proposes a new ADR. When the team accepts the new ADR, it supersedes the previous ADR."
- Scope guidance: create an ADR "for every architecturally significant decision," explicitly naming structure (e.g. microservices patterns), non-functional requirements (security, HA, fault tolerance), dependencies, interfaces (APIs, published contracts), and construction techniques (libraries, frameworks, tools, processes) as in-scope categories.
- Status lifecycle extends Nygard's four states with an explicit **Rejected** status, alongside Proposed/Accepted/Superseded, and requires a documented rejection reason "to prevent future discussions on the same topic" ([.../adr-process.html](https://docs.aws.amazon.com/prescriptive-guidance/latest/architectural-decision-records/adr-process.html)) — a formalization Nygard's original post doesn't name.
- Stated best practices: **promote ownership** (any team member can author/own an ADR, not just the lead architect); **preserve ADR history** (status → Superseded, with a change history, old ADR stays in the log); **schedule regular review meetings**, especially early in a greenfield project, tapering off after 2–3 sprints; **store centrally** (Git repo for versioning, or a wiki for broad accessibility) and reference from the main project docs page; explicitly acknowledges a limit — "the ADR process doesn't solve the issue of non-compliant legacy code" and expects deliberate follow-up (refactor or track as tech debt).

### ThoughtWorks Technology Radar

Primary source: [thoughtworks.com/en-us/radar/techniques/lightweight-architecture-decision-records](https://www.thoughtworks.com/en-us/radar/techniques/lightweight-architecture-decision-records).

- Entered the radar's **Adopt** ring in the November 2017 edition — ThoughtWorks' strongest recommendation tier, meaning "we feel strongly that the industry should be adopting these items."
- Rationale given: store the record "in source control, instead of a wiki or website," so it "remains in sync with the code itself"; valuable "for the benefit of future team members as well as for external oversight" in a world of evolutionary architecture.

### GitHub

No first-party GitHub documentation or engineering-blog post specifically prescribing ADR practice was found in this research pass — GitHub's role in the ADR ecosystem is as a hosting substrate (adr-tools, adr.github.io, and most teams' ADR directories live on GitHub), not as a publisher of ADR methodology. Treat any "GitHub recommends ADRs" claim as unsourced unless a specific first-party page turns up.

## 4. Diátaxis

**Owner / primary source:** [diataxis.fr](https://diataxis.fr/).

### The four modes and the two axes

Diátaxis organizes documentation as "a two-dimensional structure, rather than a list" ([diataxis.fr/map](https://diataxis.fr/map/)), on two axes ([diataxis.fr/compass](https://diataxis.fr/compass/)):

- **action vs. cognition** — "action: practical steps, doing" vs. "cognition: theoretical or propositional knowledge, thinking"
- **acquisition vs. application** — "acquisition: study" vs. "application: work"

The four modes sit at the four corners of that grid:

| | Acquisition (study) | Application (work) |
|---|---|---|
| **Action** (doing) | Tutorials — learning-oriented | How-to guides — goal-oriented |
| **Cognition** (thinking) | Explanation — understanding-oriented | Reference — information-oriented |

(Grid reconstructed from diataxis.fr's own axis labels and mode descriptions across [diataxis.fr](https://diataxis.fr/), [diataxis.fr/compass](https://diataxis.fr/compass/), [diataxis.fr/map](https://diataxis.fr/map/).)

### Explicit warnings against mixing modes

This is not a soft stylistic suggestion — Diátaxis names mode-mixing as the central documentation failure it exists to fix:

- On how-to guides vs. tutorials: "How-to guides are wholly distinct from tutorials. They are often confused, but the user needs that they serve are quite different. Conflating them is at the root of many difficulties that afflict documentation." A how-to guide should point at reference material rather than duplicate it: "Refer to the x reference guide for a full list of options," and should resist scope creep — "Anything else that's added distracts both you and the user... Typically, the temptations are to explain or to provide reference for completeness. Neither of these are part of guiding the user in their work" ([diataxis.fr/how-to-guides](https://diataxis.fr/how-to-guides/)).
- On explanation absorbing other modes: "One risk of explanation is that it tends to absorb other things. The writer, intent on covering the topic, feels the urge to include instruction or technical description related to it... allowing them to creep in interferes with the explanation itself, and removes them from view in the correct place" ([diataxis.fr/explanation](https://diataxis.fr/explanation/)).

### Relevance to architecture and module docs

- **Explanation** is the mode architecture documentation belongs to: it is defined as "a discursive treatment of a subject, that permits reflection... understanding-oriented," and its stated scope explicitly includes "the bigger picture," "history," "choices, alternatives, possibilities," and "why things are so — design decisions, historical reasons, technical constraints" ([diataxis.fr/explanation](https://diataxis.fr/explanation/)). This is a close conceptual match for ADRs, arc42's Solution Strategy/Architectural Decisions sections, and C4-level narrative — Diátaxis's own scope description effectively endorses ADR-style content as "explanation," while warning that such a document should not simultaneously try to be a how-to (step-by-step setup instructions) or a reference (exhaustive API/config listing).
- Diátaxis's own site, in the pages fetched, does **not** contain module-level or granularity-specific guidance (i.e., it does not explicitly say "apply this per-module" vs. "apply this per-project"). The framework is stated as organizing *content by reader need*, independent of the size of the artifact it documents, so nothing in the primary source contradicts applying it at module scope — but nothing in the primary source states that guidance directly either. Treat "apply Diátaxis per-module" as an inference, not a sourced claim.

## 5. Per-module / per-package documentation

### Go

Primary sources: [go.dev/blog/godoc](https://go.dev/blog/godoc), [go.dev/doc/comment](https://go.dev/doc/comment), [go.dev/wiki/CodeReviewComments](https://go.dev/wiki/CodeReviewComments).

- **Philosophy** (go.dev/blog/godoc): doc comments are meant to be "just good comments, the sort you would want to read even if godoc didn't exist" — documentation isn't a separate artifact from the comment, it *is* the comment, written "directly preceding its declaration, with no intervening blank line." Stated framing: "Documentation is a huge part of making software accessible and maintainable." Formatting is deliberately minimal (URLs auto-link, indented text preformats, blank lines separate paragraphs) — the tool stays out of the way rather than inventing markup.
- **Package-level doc comments** (go.dev/doc/comment): "Doc comments are comments that appear immediately before top-level package, const, func, type, and var declarations with no intervening newlines." A package comment must start "Package " as its first word/sentence — e.g. `// Package path implements utility routines for manipulating slash-separated paths.` If multiple files in a package have doc comments, godoc concatenates them into one, so in practice a package doc comment should live in exactly one file — this is the origin of the informal `doc.go` convention (a dedicated file solely for the package comment when no single file is the obvious home for it).
- **Don't document implementation, only behavior**: "Doc comments should not explain internal details such as the algorithm used in the current implementation."
- **Scope of doc comments** (go.dev/wiki/CodeReviewComments): "All top-level, exported names should have doc comments, as should non-trivial unexported type or function declarations." Comments must be full sentences: "begin with the name of the thing being described and end in a period," e.g. `// Request represents a request to run a command.`
- Go's official material, in the pages fetched, frames the package doc comment as *the* module-level document — there is no separate "architecture doc" convention prescribed by the language; the doc comment (optionally isolated in `doc.go`) is the whole story.
- Note: Go's `internal/` package convention (toolchain-enforced import boundary) is documentation-adjacent as an *enforcement*, not a *prose*, mechanism — covered separately under §7 (fitness functions), not under package-doc conventions, since it isn't discussed on the godoc/doc-comment pages themselves.

### TypeScript/JavaScript

Primary sources: [tsdoc.org](https://tsdoc.org/), [typedoc.org](https://typedoc.org/).

- **TSDoc**'s stated problem: "A single source file may get analyzed by multiple tools" (TypeDoc, DocFX, API Extractor, ESLint, etc.), "each with its own flavor of syntax extensions. This can lead to frustrating incompatibilities." TSDoc is a shared grammar so these tools agree on what a doc comment means.
- TSDoc's positioning vs. plain JSDoc: "The JSDoc grammar is not rigorously specified, but rather inferred from the behavior of a particular implementation," and most standard JSDoc tags exist to add type annotations for plain JS — "not a primary concern for a strongly-typed language such as TypeScript." TSDoc is a redesign for a typed-language audience, not a JSDoc superset for the same audience.
- TSDoc's tag model: block tags (`@param`, `@returns`, `@remarks`, `@beta`, etc.) and inline tags (`{@link ...}`), designed around three explicit goals: extensibility (custom tags), interoperability (custom tags shouldn't break other tools' parsing), and familiarity (JSDoc/Markdown-like syntax).
- **TypeDoc**: generates docs from "comments in TypeScript's source code," driven by exports — "generates documentation based on your exports" and follows re-exports across files per entry point. The pages fetched did not state an explicit "every exported symbol needs a comment" or "self-explanatory code doesn't need one" policy from TypeDoc itself — that judgment call isn't sourced from typedoc.org's own material in this pass.
- **TypeDoc's own admission that it is a lenient, not strict, TSDoc consumer**: "TypeDoc aims to be compliant with the TSDoc standard, but does not enforce it." It parses "all (or nearly all) TSDoc-compliant comments," trading strict conformance for JSDoc-project compatibility and richer Markdown — and its own docs redirect authors who want stricter TSDoc conformance checking to **api-extractor** instead ([typedoc.org/documents/Doc_Comments.TSDoc_Support.html](https://typedoc.org/documents/Doc_Comments.TSDoc_Support.html)).
- **Module-level (not per-symbol) documentation**: both specs define an explicit `@packageDocumentation` tag for this. TSDoc's own definition: it "should be used to indicate a doc comment that describes an entire NPM package (as opposed to an individual API item belonging to that package)," must be "the first `/** */` comment in the entry point `*.d.ts` file," and "a comment containing a `@packageDocumentation` tag should never be used to describe an individual API item" ([tsdoc.org/pages/tags/packagedocumentation](https://tsdoc.org/pages/tags/packagedocumentation/)). TypeDoc restates the same mechanics for its own source-level (not just `.d.ts`) input, and offers a `@module` tag as an alternative "for the same purpose when semantically clearer," which also lets an author rename a module TypeDoc guessed wrong ([typedoc.org/documents/Tags._packageDocumentation.html](https://typedoc.org/documents/Tags._packageDocumentation.html)).

### README-per-directory conventions

Primary source: [google.github.io/styleguide/docguide/READMEs.html](https://google.github.io/styleguide/docguide/READMEs.html) — this is the one genuinely first-party, canonical statement found on this topic.

- Google's rule is **top-level-per-package**, not literally every directory: "All top-level directories for a code package should have an up-to-date `README.md` file." (Contrast with a stronger "every directory" reading — Google's own text scopes it to top-level package directories.)
- Placement rule: "Unlike all other Markdown files, README.md files should not be located inside your product or library's documentation directory" — i.e., README is a landing/pointer document at the code boundary, not part of the docs tree.
- Naming is exact and enforced: "README files must be named README.md. The file name must end with the .md extension and is case sensitive."
- Minimum content: "contain a link to your user- and/or team-facing documentation," plus (per the fuller guidance) what the package does and its purpose, points of contact, deprecation/release status, and usage instructions/examples.
- Google calls out heightened importance for "package directories that provide interfaces for other teams" — i.e., READMEs matter most at team/module boundaries, which is a useful signal for where per-module docs earn their keep versus where they're just overhead.
- No other genuinely first-party (not blog-opinion) source for "README per directory" as a named convention turned up in this pass. Treat "README per directory" beyond Google's top-level-package scope as common practice without a canonical citation.

## 6. Docs-as-code / living documentation

- **Write the Docs** (owning source for the term itself): "Documentation as Code (Docs as Code) refers to a philosophy that you should be writing documentation with the same tools as code." Named practices: Issue Trackers, Version Control (Git), Plain Text Markup (Markdown/reST/AsciiDoc), Code Reviews, Automated Tests. Stated cultural effect: "a culture where writers and developers both feel ownership of documentation," including the practice of gating merges on documentation ("block merging of new features if they don't include documentation, which incentivizes developers to write about features while they are fresh") ([writethedocs.org/guide/docs-as-code](https://www.writethedocs.org/guide/docs-as-code/)).
- **Google's docguide philosophy** ([google.github.io/styleguide/docguide/philosophy.html](https://google.github.io/styleguide/docguide/philosophy.html)): "Docs thrive when they're treated like tests: a necessary chore one learns to savor because it rewards over time." On the freshness/stability trade-off specifically: "Static content is better than dynamic, because content should not depend on the features of any one server. However, fresh is better than stale." Also states a bias toward iterating over debating: "Incremental improvement is better than prolonged debate."
- **GitLab** ([docs.gitlab.com/development/documentation](https://docs.gitlab.com/development/documentation/)): runs its own documentation as a genuine docs-as-code pipeline — contributions go through a merge request with a documentation template, a bot-driven review workflow, and Vale (a prose linter) run by contributors before review. GitLab states its documentation is "the single source of truth (SSoT) for information about how to configure, use, and troubleshoot GitLab," i.e., docs-as-code isn't just a writing convenience for them, it's the authority model.
- **Microsoft**: no single first-party "docs-as-code philosophy" statement was found in this pass. Indirect evidence exists — Microsoft Learn content is Markdown, hosted and reviewed on GitHub, organized into "docsets" per the contributor guide — which is docs-as-code in practice, but this research did not turn up a first-party page that names and justifies the philosophy the way Write the Docs, Google, and GitLab do. Report this as a negative/weak result rather than citing a synthesized claim.

## 7. Keeping docs from rotting

### Google's stated policy (freshness, ownership, deletion)

Source: [google.github.io/styleguide/docguide/best_practices.html](https://google.github.io/styleguide/docguide/best_practices.html), [philosophy.html](https://google.github.io/styleguide/docguide/philosophy.html).

- "Write for humans first, computers second."
- Minimum viable docs over maximal docs: "A small set of fresh and accurate docs is better than a large assembly of 'documentation' in various states of disrepair" — with the maintenance metaphor: "Docs work best when they are alive but frequently trimmed, like a bonsai tree."
- Update discipline: "Change your documentation in the same CL as the code change" — the same commit/review, not a follow-up task.
- Deletion is explicitly sanctioned, and staleness is framed as actively harmful, not neutral: "Dead docs are bad. They misinform, they slow down, they incite despair in engineers." Google recommends removing clearly-wrong material first, then working through ambiguous content — i.e., deletion by a graduated, team-based process, not an all-at-once purge.
- The pages fetched in this pass did not state an explicit generated-vs-handwritten boundary policy (e.g., "always generate X, always handwrite Y") — that's a gap in Google's own docguide material as sourced here, not something this research can responsibly attribute to them.

### The alternative to prose: fitness functions / dependency enforcement

The throughline across these three primary sources: convert an architectural rule you would otherwise state in prose (and which will silently drift once someone violates it) into an automated check that fails a build.

- **ArchUnit** ([archunit.org](https://www.archunit.org/)): "a free, simple and extensible library for checking the architecture of your Java code using any plain Java unit test framework," letting teams "check dependencies between packages and classes, layers and slices, check for cyclic dependencies and more," pitched as something you can "Start enforcing your architecture within 30 minutes using the test setup you already have." The rule lives as an executable unit test, not a paragraph someone has to remember to update.
- **dependency-cruiser** ([github.com/sverweij/dependency-cruiser](https://github.com/sverweij/dependency-cruiser)): "Validate and visualise dependencies. With your rules" — validates dependency graphs against configured rules (circular dependencies, orphaned modules, dev-dependency leakage into production code, etc.) for JS/TS-family codebases.
- **Go's `internal/` package mechanism** ([go.dev/doc/go1.4#internalpackages](https://go.dev/doc/go1.4#internalpackages)), added in Go 1.4: solves the specific gap "there are only two forms of access: local (unexported) and global (exported)" by adding a toolchain-enforced third boundary. Rule, stated precisely: "When the go command sees an import of a package with internal in its path, it verifies that the package doing the import is within the tree rooted at the parent of the internal directory," e.g. `.../a/b/c/internal/d/e/f` "can be imported only by code in the directory tree rooted at .../a/b/c." This is enforcement by directory-naming convention plus compiler check — no prose "please don't import this" comment is needed or sufficient.

## 8. Docs written for AI agents

### AGENTS.md

Primary source: [agents.md](https://agents.md/).

- Purpose, in its own words: "a simple, open format for guiding coding agents," giving agents "a dedicated, predictable place to provide the context and instructions to help AI coding agents work on your project."
- Explicit division of labor vs. README: "README.md files are for humans: quick starts, project descriptions, and contribution guidelines." AGENTS.md "complements this by containing the extra, sometimes detailed context coding agents need: build steps, tests, and conventions that might clutter a README." The stated goal of the split is symmetric — keep agent instructions predictable *and* keep READMEs "concise and focused on human contributors."
- Recommended content areas (per the site): project overview, build/test commands, code style guidelines, testing instructions, security considerations, commit/PR guidelines.
- Adoption claim: 25+ named tools/companies (OpenAI Codex, Google Jules/Gemini CLI, Cognition Devin/Windsurf, JetBrains Junie, GitHub Copilot, VS Code, Cursor, Aider, UiPath, etc.), and "over 60k open-source projects" using the format.

### Anthropic's own guidance: CLAUDE.md

Primary source: [code.claude.com/docs/en/memory](https://code.claude.com/docs/en/memory) (redirected from docs.claude.com).

- CLAUDE.md and AGENTS.md are explicitly reconciled, not competing: "Claude Code reads CLAUDE.md, not AGENTS.md. If your repository already uses AGENTS.md for other coding agents, create a CLAUDE.md that imports it," e.g. `@AGENTS.md` followed by Claude-specific additions, or a symlink where no Claude-specific content is needed.
- Sizing guidance stated as a hard-ish rule: "target under 200 lines per CLAUDE.md file. Longer files consume more context and reduce adherence." Overflow guidance is to push content to path-scoped rules (`.claude/rules/`, loaded only for matching files) or skills (loaded on demand), not to let the always-loaded file grow.
- Style guidance mirrors general technical-writing advice but is stated as agent-specific because instructions are *context, not enforced configuration*: "Specific, concise, well-structured instructions work best," with concreteness framed as verifiability — "Use 2-space indentation" over "Format code properly," "Run `npm test` before committing" over "Test your changes." Anthropic explicitly separates what's enforced (hooks, permission settings) from what's advisory (CLAUDE.md): "Settings rules are enforced by the client regardless of what Claude decides to do. CLAUDE.md instructions shape Claude's behavior but are not a hard enforcement layer."
- Consistency matters because there's no arbitration: "if two rules contradict each other, Claude may pick one arbitrarily."

### Anthropic's engineering guidance: writing tools and skills for agents

Primary sources: [anthropic.com/engineering/writing-tools-for-agents](https://www.anthropic.com/engineering/writing-tools-for-agents), [anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills](https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills).

What's stated to differ from human-directed documentation:

- **Make implicit context explicit.** "When writing tool descriptions and specs, think of how you would describe your tool to a new hire on your team. Consider the context that you might implicitly bring — specialized query formats, definitions of niche terminology, relationships between underlying resources — and make it explicit." A human reader can ask a colleague or infer from convention; an agent, per this framing, cannot be assumed to.
- **Name things unambiguously rather than relying on shared convention.** "Input parameters should be unambiguously named: instead of a parameter named `user`, try a parameter named `user_id`."
- **Budget for token/context cost explicitly**, which has no analogue in human docs: "implement some combination of pagination, range selection, filtering, and/or truncation with sensible default parameter values for any tool responses that could use up lots of context."
- **Progressive disclosure as a structural principle, not just an organizational nicety.** Agent Skills load in layers — metadata (name/description) always loaded, `SKILL.md` body loaded when judged relevant, linked files loaded only on demand: "the skill author is able to keep the core of the skill lean, trusting that Claude will read [a linked file] only when" that specific sub-task comes up. Anthropic frames this as directly reducing wasted tokens: "If certain contexts are mutually exclusive or rarely used together, keeping the paths separate will reduce the token usage." Explicit consequence: "When the SKILL.md file becomes unwieldy, split its content into separate files and reference them" — i.e., the same overflow-to-linked-files pattern CLAUDE.md guidance uses for path-scoped rules.
- **Treat this as empirically testable, not just well-written.** "With your evaluation you can measure the impact of your prompt engineering with greater confidence. Even small refinements to tool descriptions can yield dramatic improvements" — and the skills guidance recommends the same discipline: "Monitor how Claude uses your skill in real scenarios and iterate based on observations."
- **Prefer semantic/contextual identifiers over raw technical ones.** Tool responses should "prioritize contextual relevance over flexibility, and eschew low-level technical identifiers (for example: uuid, 256px_image_url, mime_type)" — this is a departure from typical API/reference documentation, which usually *does* expose raw technical identifiers as the ground truth.

None of these sources claim agent docs should be *less* precise than human docs — the consistent theme is the opposite: agent docs must resolve ambiguity that a human reader would have silently filled in from tacit convention, and must be actively structured (metadata → body → linked files) to control what gets loaded into a bounded context window, which is a constraint prose documentation for humans doesn't have.

## Per-module documentation, specifically

This is the thinnest part of the primary-source record — there's no single authoritative "here's a module doc template" source the way there is for architecture (C4/arc42) or decisions (ADRs). What the sources above actually support, assembled:

**What genuinely first-party guidance says goes in a module-level document:**
- A **package doc comment** (Go) or a dedicated `@packageDocumentation`-tagged comment (TSDoc/TypeDoc): purpose of the package/module, written as prose that would be worth reading even without the tooling, first sentence functioning as the discoverable summary (`go.dev/doc/comment`'s "Package " convention and TSDoc's `@packageDocumentation` tag are the concrete, spec-level instances of this — both explicitly reserve one comment for module scope and forbid using it to describe an individual symbol).
- A **README at the package/module boundary** (Google's docguide): what it does, who to contact, status (deprecated/stable/etc.), and a pointer into fuller docs — explicitly *not* a place to duplicate the fuller docs.
- Exported/public API surface documentation on the declarations themselves (Go: "All top-level, exported names should have doc comments"; TypeDoc: generation is driven by exports specifically, not by the whole file).

**What should be generated instead of hand-written, per these sources:**
- API reference from exported symbols — this is TypeDoc's and godoc's entire reason to exist; hand-duplicating a function signature and its doc comment into a separate reference doc is exactly the failure mode both tools remove.
- Dependency/layering rules between modules — ArchUnit, dependency-cruiser, and Go's `internal/` all convert this from a claim in a README ("don't import X from Y") into a build-breaking check.
- Component-level and code-level diagrams, per C4's own guidance — hand-drawing these is explicitly discouraged in favor of IDE/tooling generation, especially at the Code level.

**What none of the primary sources directly resolve:** how much *narrative* ("why does this module exist, why is it shaped this way") belongs at module scope versus staying at the architecture-document scope (arc42 Building Block View, C4 Component level, or a module-scoped ADR). Diátaxis's "explanation" mode is the closest fit for that narrative content, and its warning against mode-mixing implies such narrative should live in its own document rather than inside a README (which Diátaxis would likely classify as reference/how-to territory) — but this is an inference from Diátaxis's general principles, not a sourced module-specific statement.

## Conflicts and trade-offs

- **"One README per top-level package" (Google) vs. "README per directory" (common practice, no canonical source).** Google's own text scopes the requirement to *top-level* package directories, not every directory in a tree. Treat "every directory needs a README" as folklore unless another first-party source is found — this research didn't find one.
- **C4's notation freedom vs. its notation requirements are easy to conflate.** C4 says it is "notation independent" (no mandated shapes/colors/UML-vs-boxes choice) but simultaneously mandates structural elements on every diagram (title, key/legend, explicit element types, labeled unidirectional relationships). "C4 doesn't care about notation" is true only for the visual vocabulary, not for whether a diagram is self-describing.
- **arc42 vs. Diátaxis on where "why" belongs.** arc42 embeds decision rationale inside the architecture document itself (Solution Strategy §4, Architectural Decisions §9) as sections of one continuously-maintained document. Diátaxis's stance, generalized from its mode-separation warnings, would push toward keeping "explanation" (why) content out of documents whose main job is something else (reference/how-to) — but arc42 is not a how-to or reference document, it's explicitly explanatory/narrative throughout, so this isn't a real contradiction so much as a reminder that arc42-the-document and README-the-document are different Diátaxis modes and shouldn't be governed by the same rule.
- **MADR's optional richness vs. Nygard's minimalism.** Nygard's original format is four fields. MADR 4.0 adds Decision Drivers, Considered Options, Confirmation, and detailed Pros/Cons as optional-but-present structure. Teams choosing MADR are trading Nygard's five-minute-to-write simplicity for a more rigorous, more time-consuming record — both are legitimately "the ADR format" depending which primary source you're following, and MADR itself flags most of the richer fields as optional, which is its own concession that the minimal Nygard shape remains valid.
- **"Generate, don't write" (C4 Code/Component level, ArchUnit, dependency-cruiser, TypeDoc/godoc) vs. "write, don't just generate" (Diátaxis explanation, arc42 narrative sections, ADR rationale).** These aren't actually opposed — they're addressing different content categories (structure/API surface vs. rationale/context) — but a naive reading of "prefer generated docs" could wrongly be extended to rationale content, which none of these sources claim can be generated. Nygard, arc42, and Diátaxis are unanimous that *why* is exactly the category that can't be derived from the artifact and must be deliberately written.
- **CLAUDE.md's under-200-lines guidance vs. AGENTS.md's lack of a stated size limit.** Anthropic states a specific, load-bearing size constraint tied to context-window adherence; agents.md's own material (as fetched) does not state an equivalent limit for AGENTS.md itself, even though many of the same tools consume it. This is a genuine asymmetry in the sourced guidance, not just a style difference — a large AGENTS.md may not carry the same stated adherence penalty as an oversized CLAUDE.md, per these sources, though the underlying context-budget reasoning (progressive disclosure, from the Agent Skills material) would suggest similar caution applies in practice.

## Source table

| Source | URL | Authoritative for |
|---|---|---|
| C4 model | https://c4model.com/ | The 4 diagram levels, notation rules, supplementary diagrams |
| C4 — System Context | https://c4model.com/diagrams/system-context | System Context diagram scope/audience |
| C4 — Container | https://c4model.com/diagrams/container | Container diagram scope/audience, "container" ≠ Docker |
| C4 — Component | https://c4model.com/diagrams/component | Component diagram scope, automation guidance |
| C4 — Code | https://c4model.com/diagrams/code | Code diagram level being optional/generated |
| C4 — Notation | https://c4model.com/diagrams/notation | Diagram key/title/legend requirements, notation independence |
| Structurizr | https://structurizr.com | Structurizr DSL, C4 reference implementation |
| Mermaid C4 syntax | https://mermaid.js.org/syntax/c4.html | Mermaid's C4 support and its experimental status |
| arc42 overview | https://arc42.org/overview | The 12-section template, tailorability, canvas alternative |
| arc42 docs (sections) | https://docs.arc42.org/ | Section 9 (decisions) and section 10 (quality tree/scenarios) detail |
| Nygard, "Documenting Architecture Decisions" | https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions | Original ADR structure, status lifecycle, immutability |
| adr-tools | https://github.com/npryce/adr-tools | ADR CLI conventions, numbering, superseding |
| adr.github.io | https://adr.github.io/ | ADR/AD definitions, granularity, ASR framing |
| MADR spec | https://adr.github.io/madr/ | MADR template fields and version history |
| AWS Prescriptive Guidance — ADR best practices | https://docs.aws.amazon.com/prescriptive-guidance/latest/architectural-decision-records/best-practices.html | Ownership, history, review cadence, storage, legacy-code limits |
| AWS Prescriptive Guidance — ADR process | https://docs.aws.amazon.com/prescriptive-guidance/latest/architectural-decision-records/adr-process.html | Explicit Rejected status and required rejection rationale |
| ThoughtWorks Technology Radar | https://www.thoughtworks.com/en-us/radar/techniques/lightweight-architecture-decision-records | Adopt-ring status and rationale for lightweight ADRs |
| Diátaxis | https://diataxis.fr/ | The four documentation modes |
| Diátaxis — Compass | https://diataxis.fr/compass/ | The two organizing axes (action/cognition, acquisition/application) |
| Diátaxis — How-to guides | https://diataxis.fr/how-to-guides/ | Mode-mixing warning (how-to vs. tutorial vs. reference) |
| Diátaxis — Explanation | https://diataxis.fr/explanation/ | Explanation mode definition, mode-mixing warning, closest fit to architecture docs |
| go.dev/blog/godoc | https://go.dev/blog/godoc | Godoc philosophy — doc comments as the documentation |
| go.dev/doc/comment | https://go.dev/doc/comment | Official doc-comment spec, package comment convention |
| go.dev/wiki/CodeReviewComments | https://go.dev/wiki/CodeReviewComments | Doc-comment scope and sentence-style conventions |
| go.dev — internal packages | https://go.dev/doc/go1.4#internalpackages | `internal/` toolchain-enforced boundary |
| TSDoc | https://tsdoc.org/ | TSDoc's purpose, positioning vs. JSDoc, tag model |
| TSDoc — `@packageDocumentation` tag | https://tsdoc.org/pages/tags/packagedocumentation/ | Module-level (vs. per-symbol) doc comment convention |
| TypeDoc | https://typedoc.org/ | What TypeDoc generates from (exports) |
| TypeDoc — TSDoc support | https://typedoc.org/documents/Doc_Comments.TSDoc_Support.html | TypeDoc's lenient (non-enforcing) TSDoc conformance |
| TypeDoc — `@packageDocumentation` tag | https://typedoc.org/documents/Tags._packageDocumentation.html | Module-level doc tag, `@module` alternative |
| Google docguide — index | https://google.github.io/styleguide/docguide/ | Index of Google's documentation guidance |
| Google docguide — philosophy | https://google.github.io/styleguide/docguide/philosophy.html | Freshness-vs-stability, incremental-improvement, brevity principles |
| Google docguide — best practices | https://google.github.io/styleguide/docguide/best_practices.html | Minimum viable docs, update-in-same-CL, dead-docs-are-bad |
| Google docguide — READMEs | https://google.github.io/styleguide/docguide/READMEs.html | README-per-top-level-package convention |
| Write the Docs — Docs as Code | https://www.writethedocs.org/guide/docs-as-code/ | Definition and named practices of docs-as-code |
| GitLab documentation handbook | https://docs.gitlab.com/development/documentation/ | A real docs-as-code pipeline in production (SSoT, MR-based review, Vale linting) |
| ArchUnit | https://www.archunit.org/ | Architecture-as-executable-test for Java |
| dependency-cruiser | https://github.com/sverweij/dependency-cruiser | Dependency-rule validation for JS/TS |
| AGENTS.md | https://agents.md/ | AGENTS.md purpose, README split, adoption |
| Anthropic — CLAUDE.md / memory | https://code.claude.com/docs/en/memory | CLAUDE.md structure, size guidance, AGENTS.md interop |
| Anthropic — writing tools for agents | https://www.anthropic.com/engineering/writing-tools-for-agents | Explicit-context principle, token efficiency, eval-driven iteration |
| Anthropic — Agent Skills | https://www.anthropic.com/engineering/equipping-agents-for-the-real-world-with-agent-skills | Progressive disclosure, SKILL.md structure |
