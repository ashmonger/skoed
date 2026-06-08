Feature: Documentation Site
  As a new operator landing on the skoed repo
  I want a hosted docs site at docs.skoed.io
  With install / config / cluster / troubleshooting / how-to sections
  So I'm not parsing specs/ to figure out what to run.

  Background:
    Given a static site generator
    And a published-to-Pages GitHub workflow

  @fsid:FS-DocsSiteBuildsLocally
  Scenario: `mdbook build` produces a static HTML site
    Given the docs/ tree
    When `mdbook build` runs at the repo root
    Then `docs/book/index.html` exists
    And every chapter in SUMMARY.md is rendered without warnings

  @fsid:FS-DocsSiteSearchable
  Scenario: Pagefind index is generated
    Given the docs site is built
    When `pagefind` indexes the output
    Then `docs/book/pagefind/pagefind.js` exists
    And searching for "blocklist" returns at least one hit

  @fsid:FS-DocsSiteCoversCoreFlow
  Scenario: Install + first-run + multi-node are documented
    Given the published site
    When an operator reads in order:
      | Install                 |
      | First-run setup         |
      | Cluster bootstrap       |
      | Configure a blocklist   |
    Then each chapter has a working copy-pasteable bash block

  @fsid:FS-DocsSitePublishesViaPages
  Scenario: The GitHub Pages workflow publishes on push to main
    Given the docs/ tree changes
    When the change lands on main
    Then `.github/workflows/docs.yml` fires
    And the site at docs.skoed.io (or the gh-pages branch) updates

  Non-goals:
    - Translated docs (English only)
    - API reference auto-gen — M4.5 Swagger UI already does that
    - Comment threads / forums
    - Versioned docs across previous N releases — single "latest" for v1
