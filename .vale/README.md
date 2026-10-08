# Vale styles

Vale checks the writing of the README and the website: `vale README.md site` from the top of the tree. CONTRIBUTING.md says how to get Vale, and `.vale.ini` says which rules apply and why some are turned down.

The styles here are copied verbatim from their releases, each with its license, so the rules never change under us. To update one, copy the new release's style folder over the old one and change its line below.

| Style | Source | Version | License |
|---|---|---|---|
| Google | https://github.com/errata-ai/Google | v0.7.1 | MIT |
| Readability | https://github.com/errata-ai/Readability | v0.1.1 | MIT |
| ai-tells | https://github.com/tbhb/vale-ai-tells | v1.37.0 (the `styles/ai-tells` folder) | MIT |

CCBabysitter is this project's own: sentences of at most 25 words and paragraphs of at most five, as GOV.UK keeps them, no filler transitions such as "moreover" or idioms such as "quick win", and the names of things written one way. `config/vocabularies/CCBabysitter` lists the names of things, so the other styles take them as written.
