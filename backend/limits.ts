// AI request limits for private chat/code-assist endpoints.
// Edit these values to adjust enforcement without touching Go code.
export const AI_LIMITS = {
	windowSeconds: 86400,
	perUser: 6,
	perRoom: 20,
	perIP: 10,
	perDeviceId: 10
} as const;

