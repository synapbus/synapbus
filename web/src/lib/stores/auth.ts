import { writable } from 'svelte/store';
import { auth as authApi } from '$lib/api/client';

export type User = {
	id: number;
	username: string;
	display_name: string;
	role: string;
} | null;

export const user = writable<User>(null);
export const loading = writable(true);

export async function checkAuth(): Promise<boolean> {
	try {
		loading.set(true);
		const u = await authApi.me();
		user.set(u);
		return true;
	} catch {
		user.set(null);
		return false;
	} finally {
		loading.set(false);
	}
}

export async function login(username: string, password: string): Promise<void> {
	const u = await authApi.login(username, password);
	user.set({ ...u, role: 'user' });
}

export async function logout(): Promise<void> {
	await authApi.logout();
	user.set(null);
}
