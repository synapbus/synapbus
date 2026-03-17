import { writable, get } from 'svelte/store';
import { agents as agentsApi, channels as channelsApi } from '$lib/api/client';

export type EntityInfo = {
	name: string;
	display_name?: string;
	exists: boolean;
	deleted: boolean;
};

export const agentEntities = writable<Map<string, EntityInfo>>(new Map());
export const channelEntities = writable<Map<string, EntityInfo>>(new Map());
export const entitiesLoaded = writable(false);

let loadPromise: Promise<void> | null = null;

export async function loadEntities(): Promise<void> {
	if (loadPromise) return loadPromise;
	loadPromise = _loadEntities();
	return loadPromise;
}

async function _loadEntities() {
	try {
		const [agRes, chRes] = await Promise.all([
			agentsApi.list(),
			channelsApi.list()
		]);

		const agMap = new Map<string, EntityInfo>();
		for (const a of agRes.agents ?? []) {
			agMap.set(a.name, {
				name: a.name,
				display_name: a.display_name,
				exists: true,
				deleted: a.status === 'inactive' || a.status === 'deleted'
			});
		}
		agentEntities.set(agMap);

		const chMap = new Map<string, EntityInfo>();
		for (const c of chRes.channels ?? []) {
			chMap.set(c.name, {
				name: c.name,
				exists: true,
				deleted: false
			});
		}
		channelEntities.set(chMap);
		entitiesLoaded.set(true);
	} catch {
		// silently fail — mentions will render as plain text
	} finally {
		loadPromise = null;
	}
}

export function lookupAgent(name: string): EntityInfo | null {
	const map = get(agentEntities);
	return map.get(name) || null;
}

export function lookupChannel(name: string): EntityInfo | null {
	const map = get(channelEntities);
	return map.get(name) || null;
}
