import { describe, expect, it } from 'vitest';
import { renderRichTextHtml } from './richText';

describe('renderRichTextHtml', () => {
	it('renders common conversational markdown syntax', () => {
		const html = renderRichTextHtml(`## Build Update

I finished **phase one** and cleaned up *four* issues.

- Added \`MemoryManager.kt\`
- Added [technical notes](https://example.com/notes)

See https://example.com/status for details.`);

		expect(html).toContain('<h2>Build Update</h2>');
		expect(html).toContain('<strong>phase one</strong>');
		expect(html).toContain('<em>four</em>');
		expect(html).toContain('<ul><li>Added <code>MemoryManager.kt</code></li>');
		expect(html).toContain(
			'<a href="https://example.com/notes" target="_blank" rel="noreferrer">technical notes</a>'
		);
		expect(html).toContain(
			'<a href="https://example.com/status" target="_blank" rel="noreferrer">https://example.com/status</a>'
		);
	});

	it('escapes html while still rendering fenced code blocks', () => {
		const html = renderRichTextHtml(`<script>alert(1)</script>

\`\`\`ts
const value = "<safe>";
\`\`\``);

		expect(html).toContain('&lt;script&gt;alert(1)&lt;/script&gt;');
		expect(html).toContain('<pre class="ai-rich-text-code-block">');
		expect(html).toContain('&lt;safe&gt;');
	});
});
