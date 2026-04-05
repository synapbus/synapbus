/** API client for SynapBus REST API */

class ApiError extends Error {
	status: number;
	code: string;

	constructor(status: number, code: string, message: string) {
		super(message);
		this.status = status;
		this.code = code;
		this.name = 'ApiError';
	}
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
	const opts: RequestInit = {
		method,
		headers: { 'Content-Type': 'application/json' },
		credentials: 'same-origin'
	};
	if (body) {
		opts.body = JSON.stringify(body);
	}

	const res = await fetch(path, opts);

	if (res.status === 401) {
		// On login page, pass through the actual error message
		if (typeof window !== 'undefined' && window.location.pathname.startsWith('/login')) {
			const err = await res.json().catch(() => ({ error: 'unauthorized', message: 'Invalid username or password' }));
			throw new ApiError(401, err.error || 'unauthorized', err.message || 'Invalid username or password');
		}
		// Elsewhere, redirect to login
		if (typeof window !== 'undefined') {
			window.location.href = `/login?return=${encodeURIComponent(window.location.pathname)}`;
		}
		throw new ApiError(401, 'unauthorized', 'Session expired');
	}

	if (res.status === 429) {
		const err = await res.json().catch(() => ({ message: 'Too many attempts. Please wait and try again.' }));
		throw new ApiError(429, 'rate_limited', err.message || 'Too many attempts. Please wait and try again.');
	}

	if (!res.ok) {
		const err = await res.json().catch(() => ({ error: 'unknown', message: res.statusText }));
		throw new ApiError(res.status, err.error || 'unknown', err.message || err.error_description || res.statusText);
	}

	if (res.status === 204) return undefined as T;
	return res.json();
}

// Auth
export const auth = {
	login: (username: string, password: string) =>
		request<{ id: number; username: string; display_name: string }>('POST', '/auth/login', { username, password }),
	logout: () => request<void>('POST', '/auth/logout'),
	me: () => request<{ id: number; username: string; display_name: string; role: string }>('GET', '/auth/me'),
	changePassword: (current_password: string, new_password: string) =>
		request<{ status: string }>('PUT', '/auth/password', { current_password, new_password })
};

// Messages
export const messages = {
	list: (params?: { limit?: number; status?: string; agent?: string }) => {
		const qs = new URLSearchParams();
		if (params?.limit) qs.set('limit', String(params.limit));
		if (params?.status) qs.set('status', params.status);
		if (params?.agent) qs.set('agent', params.agent);
		const q = qs.toString();
		return request<{ messages: any[]; total: number }>('GET', `/api/messages${q ? '?' + q : ''}`);
	},
	get: (id: number) => request<any>('GET', `/api/messages/${id}`),
	getReplies: (id: number) => request<{ replies: any[]; total: number }>('GET', `/api/messages/${id}/replies`),
	send: (body: { from?: string; to?: string; body: string; priority?: number; subject?: string; channel_id?: number; conversation_id?: number; reply_to?: number; attachments?: string[] }) =>
		request<any>('POST', '/api/messages', body),
	markDone: (id: number) => request<{ status: string }>('POST', `/api/messages/${id}/done`),
	search: (q: string, opts?: { limit?: number; channel?: string; agent?: string; after?: string; before?: string }) => {
		const qs = new URLSearchParams({ q });
		if (opts?.limit) qs.set('limit', String(opts.limit));
		if (opts?.channel) qs.set('channel', opts.channel);
		if (opts?.agent) qs.set('agent', opts.agent);
		if (opts?.after) qs.set('after', opts.after);
		if (opts?.before) qs.set('before', opts.before);
		return request<{ messages: any[]; query: string; total: number }>('GET', `/api/messages/search?${qs}`);
	}
};

// Conversations
export const conversations = {
	list: () => request<{ conversations: any[] }>('GET', '/api/conversations'),
	get: (id: number) => request<{ conversation: any; messages: any[] }>('GET', `/api/conversations/${id}`)
};

// DM Partners
export const dmPartners = {
	list: () => request<{ partners: any[] }>('GET', '/api/dm/partners')
};

// Agents
export const agents = {
	list: () => request<{ agents: any[] }>('GET', '/api/agents'),
	get: (name: string) => request<{ agent: any; traces: any[] }>('GET', `/api/agents/${encodeURIComponent(name)}`),
	register: (body: { name: string; display_name?: string; type?: string; capabilities?: object }) =>
		request<{ agent: any; api_key: string }>('POST', '/api/agents', body),
	update: (name: string, body: { display_name?: string; capabilities?: object }) =>
		request<{ agent: any }>('PUT', `/api/agents/${encodeURIComponent(name)}`, body),
	delete: (name: string) => request<{ status: string }>('DELETE', `/api/agents/${encodeURIComponent(name)}`),
	revokeKey: (name: string) =>
		request<{ agent: any; api_key: string }>('POST', `/api/agents/${encodeURIComponent(name)}/revoke-key`),
	messages: (name: string, limit?: number) => {
		const qs = limit ? `?limit=${limit}` : '';
		return request<{ messages: any[]; total: number }>('GET', `/api/agents/${encodeURIComponent(name)}/messages${qs}`);
	}
};

// Channels
export const channels = {
	list: () => request<{ channels: any[] }>('GET', '/api/channels'),
	get: (name: string) => request<{ channel: any; members: any[] }>('GET', `/api/channels/${encodeURIComponent(name)}`),
	create: (body: { name: string; description?: string; topic?: string; type?: string; is_private?: boolean }) =>
		request<any>('POST', '/api/channels', body),
	join: (name: string, agent?: string) =>
		request<{ status: string }>('POST', `/api/channels/${encodeURIComponent(name)}/join`, agent ? { agent } : {}),
	leave: (name: string, agent?: string) =>
		request<{ status: string }>('POST', `/api/channels/${encodeURIComponent(name)}/leave`, agent ? { agent } : {}),
	messages: (name: string, limit?: number) => {
		const qs = limit ? `?limit=${limit}` : '';
		return request<{ messages: any[]; total: number }>('GET', `/api/channels/${encodeURIComponent(name)}/messages${qs}`);
	},
	updateSettings: (name: string, settings: {
		workflow_enabled?: boolean;
		auto_approve?: boolean;
		publish_threshold?: number;
		approve_threshold?: number;
		stalemate_remind_after?: string;
		stalemate_escalate_after?: string;
	}) =>
		request<{ channel: any }>('PUT', `/api/channels/${encodeURIComponent(name)}/settings`, settings)
};

// Dead Letters
export const deadLetters = {
	list: (opts?: { acknowledged?: boolean; limit?: number }) => {
		const qs = new URLSearchParams();
		if (opts?.acknowledged) qs.set('acknowledged', 'true');
		if (opts?.limit) qs.set('limit', String(opts.limit));
		const q = qs.toString();
		return request<{ dead_letters: any[]; total: number }>('GET', `/api/dead-letters${q ? '?' + q : ''}`);
	},
	acknowledge: (id: number) =>
		request<{ acknowledged: boolean }>('POST', `/api/dead-letters/${id}/acknowledge`),
	count: () => request<{ count: number }>('GET', '/api/dead-letters/count')
};

// Webhooks
export const webhooks = {
	list: (agent?: string) => {
		const qs = agent ? `?agent=${encodeURIComponent(agent)}` : '';
		return request<{ webhooks: any[]; count: number }>('GET', `/api/webhooks${qs}`);
	},
	enable: (id: number) =>
		request<{ status: string }>('POST', `/api/webhooks/${id}/enable`),
	disable: (id: number) =>
		request<{ status: string }>('POST', `/api/webhooks/${id}/disable`),
	deliveries: (webhookId: number, opts?: { status?: string; limit?: number }) => {
		const qs = new URLSearchParams();
		if (opts?.status) qs.set('status', opts.status);
		if (opts?.limit) qs.set('limit', String(opts.limit));
		const q = qs.toString();
		return request<{ deliveries: any[]; count: number }>('GET', `/api/webhooks/${webhookId}/deliveries${q ? '?' + q : ''}`);
	}
};

// Webhook Deliveries
export const webhookDeliveries = {
	deadLetters: (opts?: { agent?: string; limit?: number }) => {
		const qs = new URLSearchParams();
		if (opts?.agent) qs.set('agent', opts.agent);
		if (opts?.limit) qs.set('limit', String(opts.limit));
		const q = qs.toString();
		return request<{ deliveries: any[]; count: number }>('GET', `/api/deliveries/dead-letters${q ? '?' + q : ''}`);
	},
	retry: (id: number) =>
		request<{ status: string }>('POST', `/api/deliveries/${id}/retry`)
};

// K8s Handlers
export const k8sHandlers = {
	list: (agent?: string) => {
		const qs = agent ? `?agent=${encodeURIComponent(agent)}` : '';
		return request<{ handlers: any[]; count: number; k8s_available: boolean }>('GET', `/api/k8s/handlers${qs}`);
	}
};

// K8s Job Runs
export const k8sJobRuns = {
	list: (opts?: { agent?: string; status?: string; limit?: number }) => {
		const qs = new URLSearchParams();
		if (opts?.agent) qs.set('agent', opts.agent);
		if (opts?.status) qs.set('status', opts.status);
		if (opts?.limit) qs.set('limit', String(opts.limit));
		const q = qs.toString();
		return request<{ job_runs: any[]; count: number }>('GET', `/api/k8s/job-runs${q ? '?' + q : ''}`);
	},
	logs: (id: number) =>
		request<{ logs: string }>('GET', `/api/k8s/job-runs/${id}/logs`)
};

// API Keys
export const apiKeys = {
	list: () => request<{ keys: any[] }>('GET', '/api/keys'),
	create: (body: { name: string; agent_id?: number; permissions?: object; allowed_channels?: string[]; read_only?: boolean; expires_at?: string }) =>
		request<{ key: any; api_key: string; mcp_config: any }>('POST', '/api/keys', body),
	revoke: (id: number) => request<{ status: string }>('DELETE', `/api/keys/${id}`),
	get: (id: number) => request<any>('GET', `/api/keys/${id}`)
};

// Notifications
export const notificationsApi = {
	unread: () =>
		request<{ channels: Record<string, number>; dms: Record<string, number> }>('GET', '/api/notifications/unread'),
	markRead: (type: 'channel' | 'dm', target: string, lastMessageId?: number) =>
		request<{ status: string }>('POST', '/api/notifications/mark-read', { type, target, last_message_id: lastMessageId })
};

// Analytics
export const analytics = {
	timeline: (span = '24h') =>
		request<{ span: string; buckets: { time: string; count: number }[]; total: number }>('GET', `/api/analytics/timeline?span=${span}`),
	topAgents: (span = '24h', limit = 5) =>
		request<{ span: string; agents: { name: string; display_name: string; count: number }[] }>('GET', `/api/analytics/top-agents?span=${span}&limit=${limit}`),
	topChannels: (span = '24h', limit = 5) =>
		request<{ span: string; channels: { name: string; count: number }[] }>('GET', `/api/analytics/top-channels?span=${span}&limit=${limit}`),
	summary: () =>
		request<{ total_agents: number; total_channels: number; total_messages: number }>('GET', '/api/analytics/summary')
};

// Version
export const version = {
	get: () => request<{ version: string; repo: string }>('GET', '/api/version')
};

// Push notifications
export const push = {
	subscribe: (subscription: { endpoint: string; key_p256dh: string; key_auth: string }) =>
		request<{ id: number; message: string }>('POST', '/api/push/subscribe', subscription),
	unsubscribe: (endpoint: string) =>
		request<{ message: string }>('DELETE', '/api/push/subscribe', { endpoint }),
	vapidKey: () => request<{ vapid_public_key: string }>('GET', '/api/push/vapid-key')
};

// User profile
export const profile = {
	update: (body: { display_name: string }) =>
		request<{ message: string; user: { id: number; username: string; display_name: string; role: string } }>('PUT', '/api/auth/profile', body)
};

// Attachments
export const attachments = {
	upload: async (file: File): Promise<{hash: string, size: number, mime_type: string, original_filename: string}> => {
		const formData = new FormData();
		formData.append('file', file);
		const response = await fetch('/api/attachments', {
			method: 'POST',
			body: formData,
			credentials: 'include'
		});
		if (!response.ok) {
			const err = await response.json().catch(() => ({error: 'Upload failed'}));
			throw new Error(err.error || err.detail || 'Upload failed');
		}
		return response.json();
	}
};

// Reactions
export const reactions = {
	toggle: (messageId: number, reaction: string, metadata?: Record<string, any>) =>
		request<{ action: string; reaction: any; reactions: any[]; workflow_state: string }>(
			'POST', `/api/messages/${messageId}/reactions`, { reaction, metadata }
		),
	get: (messageId: number) =>
		request<{ reactions: any[]; workflow_state: string }>(
			'GET', `/api/messages/${messageId}/reactions`
		)
};

// Trust Scores
export const trust = {
	get: (agentName: string) =>
		request<{ scores: Record<string, number> }>('GET', `/api/trust/${encodeURIComponent(agentName)}`)
};

// Onboarding
export const onboarding = {
	archetypes: () => request<{ archetypes: any[] }>('GET', '/api/archetypes'),
	claudeMd: async (agentName: string, archetype?: string) => {
		const qs = archetype ? `?archetype=${encodeURIComponent(archetype)}` : '';
		const res = await fetch(`/api/agents/${encodeURIComponent(agentName)}/claude-md${qs}`, { credentials: 'same-origin' });
		if (!res.ok) return '';
		return res.text();
	},
	mcpConfig: (agentName: string, apiKey?: string) => {
		const qs = apiKey ? `?api_key=${encodeURIComponent(apiKey)}` : '';
		return request<any>('GET', `/api/agents/${encodeURIComponent(agentName)}/mcp-config${qs}`);
	},
	skills: () => request<{ skills: any[] }>('GET', '/api/skills'),
	skill: async (name: string) => {
		const res = await fetch(`/api/skills/${encodeURIComponent(name)}`, { credentials: 'same-origin' });
		if (!res.ok) return '';
		return res.text();
	}
};

// Wiki
export const wiki = {
	list: (opts?: { q?: string; limit?: number }) => {
		const qs = new URLSearchParams();
		if (opts?.q) qs.set('q', opts.q);
		if (opts?.limit) qs.set('limit', String(opts.limit));
		const query = qs.toString();
		return request<{ articles: any[] }>('GET', `/api/wiki/articles${query ? '?' + query : ''}`);
	},
	get: (slug: string) =>
		request<any>('GET', `/api/wiki/articles/${slug}`),
	getHistory: (slug: string) =>
		request<{ revisions: any[] }>('GET', `/api/wiki/articles/${slug}/history`),
	getMap: () =>
		request<any>('GET', `/api/wiki/map`)
};

// Reactive Runs
export const runs = {
	list: (params?: { agent?: string; status?: string; limit?: number; offset?: number }) => {
		const qs = new URLSearchParams();
		if (params?.agent) qs.set('agent', params.agent);
		if (params?.status) qs.set('status', params.status);
		if (params?.limit) qs.set('limit', String(params.limit));
		if (params?.offset) qs.set('offset', String(params.offset));
		const q = qs.toString();
		return request<{ runs: any[]; total: number }>('GET', `/api/runs${q ? '?' + q : ''}`);
	},
	get: (id: number) => request<any>('GET', `/api/runs/${id}`),
	retry: (id: number) => request<any>('POST', `/api/runs/${id}/retry`),
	reactiveAgents: () => request<{ agents: any[] }>('GET', '/api/agents/reactive')
};

export { ApiError };
