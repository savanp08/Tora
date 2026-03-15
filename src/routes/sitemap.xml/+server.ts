import type { RequestHandler } from './$types';
import { resolveSiteOrigin } from '$lib/utils/seo';

const INDEXABLE_PATHS = ['/', '/home', '/ide'] as const;
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

function escapeXml(value: string) {
	return value
		.replace(/&/g, '&amp;')
		.replace(/</g, '&lt;')
		.replace(/>/g, '&gt;')
		.replace(/"/g, '&quot;')
		.replace(/'/g, '&apos;');
}

export const GET: RequestHandler = async ({ url, platform }) => {
	const platformSiteURL = resolvePlatformSiteURL(platform);
	const siteOrigin = resolveSiteOrigin(platformSiteURL || PUBLIC_SITE_URL_ENV, url.origin);
	const lastModifiedDate = new Date().toISOString().slice(0, 10);
	const body = `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
${INDEXABLE_PATHS.map((pathname) => {
	const absolute = `${siteOrigin}${pathname === '/' ? '' : pathname}`;
	return `  <url>
    <loc>${escapeXml(absolute)}</loc>
    <lastmod>${lastModifiedDate}</lastmod>
    <changefreq>daily</changefreq>
    <priority>${pathname === '/' ? '1.0' : '0.8'}</priority>
  </url>`;
}).join('\n')}
</urlset>`;

	return new Response(body, {
		headers: {
			'Content-Type': 'application/xml; charset=utf-8',
			'Cache-Control': 'public, max-age=3600'
		}
	});
};
