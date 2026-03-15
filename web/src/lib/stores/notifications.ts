import { writable } from 'svelte/store';

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
				const data = await res.json();
				const channels = new Map<string, number>();
				const dms = new Map<string, number>();
				if (Array.isArray(data.channels)) {
					for (const ch of data.channels) {
						if (ch.unread_count > 0) channels.set(ch.name, ch.unread_count);
					}
				}
				if (Array.isArray(data.dms)) {
					for (const dm of data.dms) {
						if (dm.unread_count > 0) dms.set(dm.agent, dm.unread_count);
					}
				}
				const counts: UnreadCounts = { channels, dms, totalUnread: 0 };
				counts.totalUnread = recalcTotal(counts);
				set(counts);
			} catch {
				// API may not be available yet — silently ignore
			}
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
