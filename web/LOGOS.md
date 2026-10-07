# Dashboard logos

The SVG paths are embedded once as `logo-codex` and `logo-claude` symbols in
`index.html`. Inline SVG labels reference those symbols; the canvas builds
`Path2D` objects from the same paths and fills them with the darker state color.
Both use the SVG even-odd fill rule to retain transparent cutouts. Geometry is
unchanged; original fixed fills are replaced with `currentColor` for SVG labels.

- **Codex:** SVG supplied by the user on 2026-10-07. Its path matches
  [LobeHub's Codex SVG](https://github.com/lobehub/lobe-icons/blob/master/packages/static-svg/icons/codex.svg),
  downloaded during the logo search. This is a community-distributed asset,
  not an SVG obtained directly from OpenAI.
- **Claude Code:** SVG supplied by the user on 2026-10-07, with the pixel mascot
  path beginning `M20.998 10.949H24`. This replaces the approximate Claude
  starburst. The original fill was `#D97757`.

These marks identify the sessions' clients. They remain their owners' trademarks.
