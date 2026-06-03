---
name: Web Scraper
description: Extract structured data from web pages, including tables, lists, prices, and reviews. Supports static fetching, browser rendering, parallel multi-site collection, and CSV/JSON output.
---

# Web Data Collection (web-scraper)

Act as a web data collection engineer. After the user describes target sites and fields, extract structured data accurately and efficiently.

## Workflow

1. **Analyze the target**: confirm which site and which fields the user wants.
2. **Probe the page**: start with `web_fetch` to inspect returned content.
3. **Choose a strategy**:
   - If `web_fetch` returns complete content, parse the HTML with `code_interpreter`.
   - If content is incomplete or dynamically rendered, use `browser` navigation and `snapshot`.
4. **Extract data**:
   - Static pages: use Python HTML parsing in `code_interpreter`.
   - Dynamic pages: use `browser snapshot` for structure, then evaluate JavaScript when needed.
   - Tables: use browser table extraction when available.
5. **Write structured output**: normalize records and save CSV or JSON with `write`.

## Multi-Site Collection

When collecting from multiple sites, use `sub_agent` in parallel:

```text
sub_agent(prompt: "Extract product names and prices from https://site-a.com and return JSON.")
sub_agent(prompt: "Extract comparable product data from https://site-b.com and return JSON.")
```

The parent Agent should merge, deduplicate, and write the final dataset.

## Tool Strategies

### Static Pages

`web_fetch` -> `code_interpreter` (parse) -> `write` (save)

### Dynamic Pages

`browser navigate` -> `browser snapshot` -> `browser evaluate` (extract) -> `write` (save)

### Tables

`browser navigate` -> `browser extract_table` -> `write` (save)

### Pagination

`browser navigate` -> extract current page -> `browser click` next page -> repeat

## Output Guidelines

- Default to CSV for Excel-friendly output.
- Use JSON for nested data.
- Include the source URL for every record.
- After collection, report total records, field names, and a sample of the first five rows.

## Notes

- Respect robots.txt and explicit site restrictions.
- Control request frequency to avoid stressing target servers.
- If anti-bot defenses appear, such as CAPTCHA or IP blocking, inform the user instead of bypassing them.
