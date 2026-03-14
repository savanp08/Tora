import { createHmac, timingSafeEqual } from 'node:crypto';
import { env } from '$env/dynamic/private';
import type { Handle } from '@sveltejs/kit';

const AUTH_COOKIE_NAME = 'tora_auth';
const FALLBACK_ROLE = 'member' as const;

type JwtPayload = {
	userId?: unknown;
	email?: unknown;
	username?: unknown;
	exp?: unknown;
};

type AuthenticatedUser = {
	id: string;
	email: string;
	name: string;
	avatarUrl: string;
	role: 'admin' | 'member' | 'viewer';
};

function authDebugLog(_event: string, _payload?: Record<string, unknown>) {
	// Auth route debug logs intentionally disabled.
}

function base64urlDecode(segment: string) {
	const normalized = segment.replace(/-/g, '+').replace(/_/g, '/');
	const padding = '='.repeat((4 - (normalized.length % 4)) % 4);
	return Buffer.from(normalized + padding, 'base64').toString('utf8');
}

function deriveNameFromEmail(email: string) {
	const prefix = email.split('@')[0]?.trim() || 'User';
	const cleaned = prefix
		.split(/[._-]+/)
		.filter(Boolean)
		.map((part) => part.slice(0, 1).toUpperCase() + part.slice(1))
		.join(' ')
		.trim();
	return cleaned || 'User';
}

function verifyHS256(token: string, secret: string) {
	const parts = token.split('.');
	if (parts.length !== 3) {
		return false;
	}
	const [headerSegment, payloadSegment, signatureSegment] = parts;
	try {
		const headerRaw = base64urlDecode(headerSegment);
		const header = JSON.parse(headerRaw) as { alg?: unknown };
		if (header.alg !== 'HS256') {
			return false;
		}
	} catch {
		return false;
	}

	const signedContent = `${headerSegment}.${payloadSegment}`;
	const expectedSignature = createHmac('sha256', secret)
		.update(signedContent)
		.digest('base64url');
	const actual = Buffer.from(signatureSegment);
	const expected = Buffer.from(expectedSignature);
	if (actual.length !== expected.length) {
		return false;
	}
	return timingSafeEqual(actual, expected);
}

function parseUserFromToken(token: string): AuthenticatedUser | null {
	authDebugLog('Token parse started.', {
		tokenLength: token.length
	});
	const secret = env.APP_SECRET_KEY?.trim();
	if (!secret) {
		authDebugLog('Token parse failed: APP_SECRET_KEY is missing.');
		return null;
	}
	if (!verifyHS256(token, secret)) {
		authDebugLog('Token parse failed: signature validation failed.');
		return null;
	}

	const payloadSegment = token.split('.')[1];
	try {
		const payloadRaw = base64urlDecode(payloadSegment);
		const payload = JSON.parse(payloadRaw) as JwtPayload;
		const userId = typeof payload.userId === 'string' ? payload.userId.trim() : '';
		const email = typeof payload.email === 'string' ? payload.email.trim().toLowerCase() : '';
		const username = typeof payload.username === 'string' ? payload.username.trim() : '';
		const exp = typeof payload.exp === 'number' ? payload.exp : Number(payload.exp);

		if (!userId || !Number.isFinite(exp)) {
			authDebugLog('Token parse failed: payload missing required fields.', {
				hasUserId: Boolean(userId),
				hasExp: Number.isFinite(exp)
			});
			return null;
		}
		const nowSeconds = Math.floor(Date.now() / 1000);
		if (exp <= nowSeconds) {
			authDebugLog('Token parse failed: token expired.', {
				exp,
				nowSeconds
			});
			return null;
		}

		authDebugLog('Token parse succeeded.', {
			userId,
			email,
			username
		});
		return {
			id: userId,
			email,
			name: username || deriveNameFromEmail(email),
			avatarUrl: '',
			role: FALLBACK_ROLE
		};
	} catch {
		authDebugLog('Token parse failed: payload JSON decode error.');
		return null;
	}
}



export const handle: Handle = async ({ event, resolve }) => {
	const token = event.cookies.get(AUTH_COOKIE_NAME)?.trim() || '';
	authDebugLog('Incoming request auth check.', {
		pathname: event.url.pathname,
		hasAuthCookie: token.length > 0,
		authCookieLength: token.length
	});
	event.locals.user = token ? parseUserFromToken(token) : null;
	authDebugLog('Request auth check complete.', {
		pathname: event.url.pathname,
		isAuthenticated: Boolean(event.locals.user),
		userId: event.locals.user?.id ?? ''
	});
	const response = await resolve(event);
	response.headers.set('Cross-Origin-Embedder-Policy', 'credentialless');
	response.headers.set('Cross-Origin-Opener-Policy', 'same-origin');
	return response;
};
