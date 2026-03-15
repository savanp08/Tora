const DEFAULT_SITE_NAME = 'Tora';
const DEFAULT_SITE_DESCRIPTION =
	'Collaborative temporary chat rooms, online IDE execution, shared code canvas, and free draw board for fast team work.';

function trimTrailingSlash(value: string) {
	return value.endsWith('/') ? value.slice(0, -1) : value;
}

function normalizePathname(pathname: string) {
	if (!pathname || pathname === '/') {
		return '/';
	}
	const normalized = pathname.startsWith('/') ? pathname : `/${pathname}`;
	return normalized.length > 1 && normalized.endsWith('/')
		? normalized.slice(0, -1)
		: normalized;
}

export function resolveSiteOrigin(siteOrigin: string, fallbackOrigin: string) {
	const candidate = (siteOrigin || '').trim();
	if (!candidate) {
		return trimTrailingSlash(fallbackOrigin);
	}
	const withProtocol = /^[a-zA-Z][a-zA-Z\d+.-]*:\/\//.test(candidate)
		? candidate
		: `https://${candidate}`;
	try {
		const parsed = new URL(withProtocol);
		return trimTrailingSlash(parsed.origin);
	} catch {
		return trimTrailingSlash(fallbackOrigin);
	}
}

export function buildCanonicalURL(siteOrigin: string, pathname: string) {
	const base = trimTrailingSlash(siteOrigin);
	return `${base}${normalizePathname(pathname)}`;
}

export function buildSoftwareApplicationSchema(options: {
	name: string;
	description: string;
	url?: string;
	category?: string;
	operatingSystem?: string;
}) {
	const schema: Record<string, string> = {
		'@context': 'https://schema.org',
		'@type': 'SoftwareApplication',
		name: options.name,
		description: options.description,
		applicationCategory: options.category || 'DeveloperApplication',
		operatingSystem: options.operatingSystem || 'Web'
	};
	const normalizedUrl = (options.url || '').trim();
	if (normalizedUrl) {
		try {
			schema.url = new URL(normalizedUrl).toString();
		} catch {
			// Keep schema valid even when an absolute URL is not configured.
		}
	}
	return JSON.stringify(schema);
}

export { DEFAULT_SITE_NAME, DEFAULT_SITE_DESCRIPTION };
