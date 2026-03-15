import { writable, get } from 'svelte/store';

export interface UnreadCounts {
	channels: Map<string, number>;
	dms: Map<string, number>;
	totalUnread: number;
}

function createNotificationStore() {
	const { subscribe, set, update } = writable<UnreadCounts>({
		channels: new Map(),
		dms: new Map(),
		totalUnread: 0
	});

	function recalcTotal(counts: UnreadCounts): number {
		let total = 0;
		for (const v of counts.channels.values()) total += v;
		for (const v of counts.dms.values()) total += v;
		return total;
	}

	return {
		subscribe,

		/** Initialize from the backend GET /api/notifications/unread */
		async initialize() {
			try {
				const res = await fetch('/api/notifications/unread', { credentials: 'same-origin' });
				if (!res.ok) return;
				const data: { channels?: Record<string, number>; dms?: Record<string, number> } = await res.json();
				const channels = new Map(Object.entries(data.channels ?? {}));
				const dms = new Map(Object.entries(data.dms ?? {}));
				const counts: UnreadCounts = { channels, dms, totalUnread: 0 };
				counts.totalUnread = recalcTotal(counts);
				set(counts);
			} catch {
				// API may not be available yet — silently ignore
			}
		},

		/** Get unread count for a channel */
		channelUnread(name: string): number {
			return get({ subscribe }).channels.get(name) ?? 0;
		},

		/** Get unread count for a DM agent */
		dmUnread(name: string): number {
			return get({ subscribe }).dms.get(name) ?? 0;
		},

		/** Get total unread count */
		get totalUnread(): number {
			return get({ subscribe }).totalUnread;
		},

		/** Increment unread count for a channel or DM */
		incrementUnread(type: 'channel' | 'dm', target: string) {
			update((counts) => {
				const map = type === 'channel' ? counts.channels : counts.dms;
				map.set(target, (map.get(target) ?? 0) + 1);
				counts.totalUnread = recalcTotal(counts);
				return counts;
			});
		},

		/** Set exact unread count for a channel or DM */
		setUnread(type: 'channel' | 'dm', target: string, count: number) {
			update((counts) => {
				const map = type === 'channel' ? counts.channels : counts.dms;
				if (count > 0) {
					map.set(target, count);
				} else {
					map.delete(target);
				}
				counts.totalUnread = recalcTotal(counts);
				return counts;
			});
		},

		/** Mark a channel or DM as read — POST to backend and clear local count */
		async markAsRead(type: 'channel' | 'dm', target: string, lastMessageId?: number) {
			try {
				await fetch('/api/notifications/mark-read', {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					credentials: 'same-origin',
					body: JSON.stringify({ type, target, last_message_id: lastMessageId })
				});
			} catch {
				// Best effort — clear locally regardless
			}
			update((counts) => {
				const map = type === 'channel' ? counts.channels : counts.dms;
				map.delete(target);
				counts.totalUnread = recalcTotal(counts);
				return counts;
			});
		},

		/** Reset all counts (e.g. on logout) */
		reset() {
			set({ channels: new Map(), dms: new Map(), totalUnread: 0 });
		}
	};
}

export const notifications = createNotificationStore();
