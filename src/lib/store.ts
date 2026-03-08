import { writable } from 'svelte/store';
import type { User } from './types';
import { getDefaultSessionRoomPreferences } from './utils/sessionPreferences';

export const currentUser = writable<User | null>(null);
export const authToken = writable<string | null>(null);
export const isDarkMode = writable<boolean>(false);
export const activeRoomPassword = writable<string>('');
const defaultSessionRoomPreferences = getDefaultSessionRoomPreferences();
export const sessionAIEnabled = writable<boolean>(defaultSessionRoomPreferences.aiEnabled);
export const sessionE2EEnabled = writable<boolean>(defaultSessionRoomPreferences.e2eEnabled);
