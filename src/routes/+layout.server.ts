import type { LayoutServerLoad } from './$types';
import {
	DEFAULT_SITE_DESCRIPTION,
	DEFAULT_SITE_NAME,
	buildCanonicalURL,
	resolveSiteOrigin
} from '$lib/utils/seo';

const PUBLIC_SITE_URL_ENV = (process.env.PUBLIC_SITE_URL || '').trim();

function resolvePlatformSiteURL(platform: unknown) {
	if (!platform || typeof platform !== 'object') {
		return '';
	}
	const platformRecord = platform as Record<string, unknown>;
	const env = platformRecord.env;
	if (!env || typeof env !== 'object') {
		return '';
	}
	const envRecord = env as Record<string, unknown>;
	return typeof envRecord.PUBLIC_SITE_URL === 'string' ? envRecord.PUBLIC_SITE_URL.trim() : '';
}

export const load: LayoutServerLoad = async ({ url, platform }) => {
	const platformSiteURL = resolvePlatformSiteURL(platform);
	const siteOrigin = resolveSiteOrigin(platformSiteURL || PUBLIC_SITE_URL_ENV, url.origin);
	const canonicalUrl = buildCanonicalURL(siteOrigin, url.pathname);
	const websiteSchemaJson = JSON.stringify({
		'@context': 'https://schema.org',
		'@type': 'WebSite',
		name: DEFAULT_SITE_NAME,
		alternateName: 'Tora Workspace',
		url: siteOrigin,
		description: DEFAULT_SITE_DESCRIPTION
	});
	const ogImageUrl = `${siteOrigin}/og-image.png`;
	return {
		siteOrigin,
		canonicalUrl,
		websiteSchemaJson,
		ogImageUrl
	};
};
