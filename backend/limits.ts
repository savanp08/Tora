// AI request limits for private chat/code-assist endpoints.
// Edit these values to adjust enforcement without touching Go code.
export const AI_LIMITS = {
	windowSeconds: 86400,
	perUser: 2,
	perRoom: 10,
	perIP: 5,
	perDeviceId: 5
} as const;

