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
		// Redirect to login on auth failure
		if (typeof window !== 'undefined' && !window.location.pathname.startsWith('/login')) {
			window.location.href = `/login?return=${encodeURIComponent(window.location.pathname)}`;
		}
		throw new ApiError(401, 'unauthorized', 'Session expired');
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
	send: (body: { from?: string; to?: string; body: string; priority?: number; subject?: string; channel_id?: number }) =>
		request<any>('POST', '/api/messages', body),
	markDone: (id: number) => request<{ status: string }>('POST', `/api/messages/${id}/done`),
	search: (q: string, limit?: number) => {
		const qs = new URLSearchParams({ q });
		if (limit) qs.set('limit', String(limit));
		return request<{ messages: any[]; query: string; total: number }>('GET', `/api/messages/search?${qs}`);
	}
};

// Conversations
export const conversations = {
	list: () => request<{ conversations: any[] }>('GET', '/api/conversations'),
	get: (id: number) => request<{ conversation: any; messages: any[] }>('GET', `/api/conversations/${id}`)
};

// Agents
export const agents = {
	list: () => request<{ agents: any[] }>('GET', '/api/agents'),
	get: (name: string) => request<{ agent: any; traces: any[] }>('GET', `/api/agents/${encodeURIComponent(name)}`),
	register: (body: { name: string; display_name?: string; type?: string; capabilities?: object }) =>
		request<{ agent: any; api_key: string }>('POST', '/api/agents', body),
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
	}
};

// API Keys
export const apiKeys = {
	list: () => request<{ keys: any[] }>('GET', '/api/keys'),
	create: (body: { name: string; agent_id?: number; permissions?: object; allowed_channels?: string[]; read_only?: boolean; expires_at?: string }) =>
		request<{ key: any; api_key: string; mcp_config: any }>('POST', '/api/keys', body),
	revoke: (id: number) => request<{ status: string }>('DELETE', `/api/keys/${id}`),
	get: (id: number) => request<any>('GET', `/api/keys/${id}`)
};

export { ApiError };
