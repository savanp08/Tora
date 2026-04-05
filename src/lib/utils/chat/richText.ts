const CODE_FENCE_PATTERN = /```([^\n`]*)\n?([\s\S]*?)```/g;
const MARKDOWN_LINK_PATTERN = /^\[([^\]\n]+)\]\(([^)\s]+)\)/;
const BARE_URL_PATTERN = /^(https?:\/\/[^\s<]+|www\.[^\s<]+)/i;

type ListState = {
	type: 'ul' | 'ol';
	items: string[];
};

export function renderRichTextHtml(value: string) {
	const normalized = normalizeRichTextSource(value);
	if (!normalized) {
		return '';
	}

	const parts: string[] = [];
	let lastIndex = 0;
	CODE_FENCE_PATTERN.lastIndex = 0;

	for (let match = CODE_FENCE_PATTERN.exec(normalized); match; match = CODE_FENCE_PATTERN.exec(normalized)) {
		const [token, language, code] = match;
		if (match.index > lastIndex) {
			parts.push(renderTextBlocks(normalized.slice(lastIndex, match.index)));
		}
		parts.push(renderCodeBlock(language, code));
		lastIndex = match.index + token.length;
	}

	if (lastIndex < normalized.length) {
		parts.push(renderTextBlocks(normalized.slice(lastIndex)));
	}

	return parts.join('');
}

function normalizeRichTextSource(value: string) {
	return String(value || '').replace(/\r\n/g, '\n').replace(/\r/g, '\n').trim();
}

function renderCodeBlock(language: string, code: string) {
	const safeLanguage = escapeHtml(language.trim());
	const languageBadge = safeLanguage
		? `<div class="ai-rich-text-code-language">${safeLanguage}</div>`
		: '';
	return `<pre class="ai-rich-text-code-block">${languageBadge}<code>${escapeHtml(
		code.replace(/\n$/, '')
	)}</code></pre>`;
}

function renderTextBlocks(value: string) {
	const lines = value.split('\n');
	const html: string[] = [];
	let paragraphLines: string[] = [];
	let listState: ListState | null = null;
	let quoteLines: string[] = [];

	const flushParagraph = () => {
		if (paragraphLines.length === 0) {
			return;
		}
		html.push(`<p>${renderInline(paragraphLines.join('\n'))}</p>`);
		paragraphLines = [];
	};

	const flushList = () => {
		if (!listState || listState.items.length === 0) {
			listState = null;
			return;
		}
		const itemsHtml = listState.items
			.map((item) => `<li>${renderInline(item)}</li>`)
			.join('');
		html.push(`<${listState.type}>${itemsHtml}</${listState.type}>`);
		listState = null;
	};

	const flushQuote = () => {
		if (quoteLines.length === 0) {
			return;
		}
		html.push(`<blockquote>${renderTextBlocks(quoteLines.join('\n'))}</blockquote>`);
		quoteLines = [];
	};

	for (const line of lines) {
		const trimmed = line.trim();
		const quoteMatch = line.match(/^\s*>\s?(.*)$/);
		if (quoteMatch) {
			flushParagraph();
			flushList();
			quoteLines.push(quoteMatch[1]);
			continue;
		}
		flushQuote();

		if (!trimmed) {
			flushParagraph();
			flushList();
			continue;
		}

		if (/^\s*([-*_])\1{2,}\s*$/.test(line)) {
			flushParagraph();
			flushList();
			html.push('<hr />');
			continue;
		}

		const headingMatch = line.match(/^\s{0,3}(#{1,6})\s+(.*)$/);
		if (headingMatch) {
			flushParagraph();
			flushList();
			const level = Math.min(6, headingMatch[1].length);
			html.push(`<h${level}>${renderInline(headingMatch[2].trim())}</h${level}>`);
			continue;
		}

		const unorderedMatch = line.match(/^\s*[-*+]\s+(.*)$/);
		if (unorderedMatch) {
			flushParagraph();
			if (!listState || listState.type !== 'ul') {
				flushList();
				listState = { type: 'ul', items: [] };
			}
			listState.items.push(unorderedMatch[1]);
			continue;
		}

		const orderedMatch = line.match(/^\s*\d+\.\s+(.*)$/);
		if (orderedMatch) {
			flushParagraph();
			if (!listState || listState.type !== 'ol') {
				flushList();
				listState = { type: 'ol', items: [] };
			}
			listState.items.push(orderedMatch[1]);
			continue;
		}

		if (listState && /^\s{2,}\S/.test(line) && listState.items.length > 0) {
			listState.items[listState.items.length - 1] += `\n${trimmed}`;
			continue;
		}

		flushList();
		paragraphLines.push(line);
	}

	flushParagraph();
	flushList();
	flushQuote();

	return html.join('');
}

function renderInline(value: string): string {
	let html = '';
	let index = 0;

	while (index < value.length) {
		const linkMatch = value.slice(index).match(MARKDOWN_LINK_PATTERN);
		if (linkMatch) {
			const href = normalizeLinkHref(linkMatch[2]);
			if (href) {
				html += `<a href="${escapeAttribute(href)}" target="_blank" rel="noreferrer">${renderInline(
					linkMatch[1]
				)}</a>`;
				index += linkMatch[0].length;
				continue;
			}
		}

		const bareUrlMatch = readBareUrl(value, index);
		if (bareUrlMatch) {
			html += `<a href="${escapeAttribute(bareUrlMatch.href)}" target="_blank" rel="noreferrer">${escapeHtml(
				bareUrlMatch.label
			)}</a>`;
			index = bareUrlMatch.nextIndex;
			continue;
		}

		const codeSpanMatch = readDelimitedSegment(value, index, '`');
		if (codeSpanMatch) {
			html += `<code>${escapeHtml(codeSpanMatch.content)}</code>`;
			index = codeSpanMatch.nextIndex;
			continue;
		}

		const strongMatch =
			readDelimitedSegment(value, index, '**') ?? readDelimitedSegment(value, index, '__');
		if (strongMatch) {
			html += `<strong>${renderInline(strongMatch.content)}</strong>`;
			index = strongMatch.nextIndex;
			continue;
		}

		const strikeMatch = readDelimitedSegment(value, index, '~~');
		if (strikeMatch) {
			html += `<del>${renderInline(strikeMatch.content)}</del>`;
			index = strikeMatch.nextIndex;
			continue;
		}

		const emphasisMatch = readEmphasisSegment(value, index);
		if (emphasisMatch) {
			html += `<em>${renderInline(emphasisMatch.content)}</em>`;
			index = emphasisMatch.nextIndex;
			continue;
		}

		if (value[index] === '\n') {
			html += '<br />';
			index += 1;
			continue;
		}

		html += escapeHtml(value[index]);
		index += 1;
	}

	return html;
}

function readBareUrl(value: string, index: number) {
	const previousChar = index > 0 ? value[index - 1] : '';
	if (previousChar && !/\s|[(>]/.test(previousChar)) {
		return null;
	}

	const match = value.slice(index).match(BARE_URL_PATTERN);
	if (!match) {
		return null;
	}

	const rawUrl = trimTrailingUrlPunctuation(match[1]);
	const href = normalizeLinkHref(rawUrl);
	if (!href) {
		return null;
	}

	return {
		href,
		label: rawUrl,
		nextIndex: index + rawUrl.length
	};
}

function readDelimitedSegment(value: string, index: number, delimiter: string) {
	if (!value.startsWith(delimiter, index)) {
		return null;
	}

	const searchStart = index + delimiter.length;
	const closingIndex = value.indexOf(delimiter, searchStart);
	if (closingIndex <= searchStart) {
		return null;
	}

	const content = value.slice(searchStart, closingIndex);
	if (!content.trim()) {
		return null;
	}

	return {
		content,
		nextIndex: closingIndex + delimiter.length
	};
}

function readEmphasisSegment(value: string, index: number) {
	if (value[index] !== '*') {
		return null;
	}
	if (value.startsWith('**', index)) {
		return null;
	}

	const previousChar = index > 0 ? value[index - 1] : '';
	const nextChar = value[index + 1] ?? '';
	if ((previousChar && /\w/.test(previousChar)) || !nextChar.trim()) {
		return null;
	}

	for (let cursor = index + 1; cursor < value.length; cursor += 1) {
		if (value[cursor] !== '*' || value[cursor - 1] === '\\') {
			continue;
		}
		const closingNextChar = value[cursor + 1] ?? '';
		if (closingNextChar === '*') {
			continue;
		}
		const content = value.slice(index + 1, cursor);
		if (!content.trim()) {
			return null;
		}
		return {
			content,
			nextIndex: cursor + 1
		};
	}

	return null;
}

function trimTrailingUrlPunctuation(value: string) {
	let trimmed = value;
	while (/[),.;!?]$/.test(trimmed)) {
		if (trimmed.endsWith(')')) {
			const openCount = (trimmed.match(/\(/g) || []).length;
			const closeCount = (trimmed.match(/\)/g) || []).length;
			if (closeCount <= openCount) {
				break;
			}
		}
		trimmed = trimmed.slice(0, -1);
	}
	return trimmed;
}

function normalizeLinkHref(value: string) {
	const trimmed = value.trim();
	if (!trimmed) {
		return '';
	}
	if (/^https?:\/\//i.test(trimmed) || /^mailto:/i.test(trimmed)) {
		return trimmed;
	}
	if (/^www\./i.test(trimmed)) {
		return `https://${trimmed}`;
	}
	return '';
}

function escapeHtml(value: string) {
	return value
		.replace(/&/g, '&amp;')
		.replace(/</g, '&lt;')
		.replace(/>/g, '&gt;')
		.replace(/"/g, '&quot;')
		.replace(/'/g, '&#39;');
}

function escapeAttribute(value: string) {
	return escapeHtml(value);
}
