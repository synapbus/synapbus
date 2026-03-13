import { writable } from 'svelte/store';

export const activeThread = writable<{ messageId: number; conversationId: number } | null>(null);

export function openThread(messageId: number, conversationId: number) {
	activeThread.set({ messageId, conversationId });
}

export function closeThread() {
	activeThread.set(null);
}
