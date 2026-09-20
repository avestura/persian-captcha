# Bundled webfonts

Persian text renders correctly in every modern browser without a bundled font,
but the result depends on whatever the visitor's system provides, and on
Windows that is often Tahoma: legible, but a poor match for a modern page.
Shipping a font makes the widget look the same everywhere.

The widget asks for **Vazirmatn**, a libre Persian typeface released under the
SIL Open Font License 1.1. It is not committed here, because vendoring a font
into a source repository is a licensing decision each operator should make
deliberately rather than inherit.

## Adding it

Drop a variable WOFF2 into this directory:

```sh
./scripts/fetch-fonts.sh          # downloads Vazirmatn[wght].woff2 here
```

Any file named `*.woff2` in this directory is served from
`/v1/font/<filename>` and referenced automatically by the Persian locale. If no
file is present the widget falls back to the system stack declared in
`internal/i18n/locales/fa.json`, which is the default and is perfectly usable.

## Licence note

If you add Vazirmatn, keep its `OFL.txt` alongside the font file. The OFL
requires the licence to travel with the font, including in binaries that embed
it — and this directory is compiled into the service binary by `go:embed`.
