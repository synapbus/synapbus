/** SSE client with auto-reconnect and exponential backoff */

type SSEHandler = (event: { type: string; data: any }) => void;

export class SSEClient {
	private url: string;
	private eventSource: EventSource | null = null;
	private handlers: SSEHandler[] = [];
	private reconnectDelay = 1000;
	private maxReconnectDelay = 30000;
	private shouldReconnect = true;

	constructor(url: string = '/api/events') {
		this.url = url;
	}

	connect() {
		if (this.eventSource) {
			this.eventSource.close();
		}

		this.eventSource = new EventSource(this.url, { withCredentials: true });

		this.eventSource.onopen = () => {
			this.reconnectDelay = 1000; // Reset backoff on successful connection
		};

		// Listen for typed events
		const eventTypes = ['connected', 'new_message', 'message_updated', 'agent_connected', 'agent_disconnected', 'heartbeat', 'unread_update'];
		for (const type of eventTypes) {
			this.eventSource.addEventListener(type, (e: MessageEvent) => {
				try {
					const data = JSON.parse(e.data);
					this.emit({ type, data });
				} catch {
					// Ignore parse errors
				}
			});
		}

		this.eventSource.onerror = () => {
			this.eventSource?.close();
			this.eventSource = null;

			if (this.shouldReconnect) {
				setTimeout(() => this.connect(), this.reconnectDelay);
				this.reconnectDelay = Math.min(this.reconnectDelay * 2, this.maxReconnectDelay);
			}
		};
	}

	onEvent(handler: SSEHandler) {
		this.handlers.push(handler);
		return () => {
			this.handlers = this.handlers.filter((h) => h !== handler);
		};
	}

	private emit(event: { type: string; data: any }) {
		for (const handler of this.handlers) {
			handler(event);
		}
	}

	disconnect() {
		this.shouldReconnect = false;
		this.eventSource?.close();
		this.eventSource = null;
	}

	get connected(): boolean {
		return this.eventSource?.readyState === EventSource.OPEN;
	}
}
