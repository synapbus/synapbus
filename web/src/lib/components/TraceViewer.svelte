<script lang="ts">
	type Trace = {
		id: number;
		agent_name: string;
		action: string;
		details: any;
		error?: string;
		timestamp: string;
	};

	let { traces = [] }: { traces: Trace[] } = $props();
	let expandedId = $state<number | null>(null);

	function formatTime(iso: string): string {
		return new Date(iso).toLocaleString();
	}

	function toggle(id: number) {
		expandedId = expandedId === id ? null : id;
	}
</script>

{#if traces.length === 0}
	<div class="text-center py-8 text-text-secondary">
		<p class="text-sm">No activity traces</p>
	</div>
{:else}
	<div class="divide-y divide-border">
		{#each traces as trace (trace.id)}
			<div>
				<button
					class="w-full flex items-center justify-between text-left px-4 py-2.5 hover:bg-bg-tertiary/50 transition-colors"
					onclick={() => toggle(trace.id)}
				>
					<div class="flex items-center gap-3">
						<span class="badge bg-bg-tertiary text-text-secondary font-mono text-[11px]">
							{trace.action}
						</span>
						{#if trace.error}
							<span class="badge-failed">error</span>
						{/if}
					</div>
					<span class="text-[11px] text-text-secondary">{formatTime(trace.timestamp)}</span>
				</button>
				{#if expandedId === trace.id}
					<div class="mx-4 mb-3 p-3 bg-bg-primary rounded-lg border border-border">
						<pre class="text-xs text-text-primary/80 overflow-x-auto whitespace-pre-wrap font-mono">{JSON.stringify(trace.details, null, 2)}</pre>
						{#if trace.error}
							<p class="mt-2 text-xs text-accent-red">Error: {trace.error}</p>
						{/if}
					</div>
				{/if}
			</div>
		{/each}
	</div>
{/if}
