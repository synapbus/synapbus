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
	<div class="text-center py-8 text-gray-500 dark:text-gray-400">
		<p>No activity traces</p>
	</div>
{:else}
	<div class="divide-y divide-gray-100 dark:divide-gray-700">
		{#each traces as trace (trace.id)}
			<div class="py-2">
				<button
					class="w-full flex items-center justify-between text-left px-3 py-2 rounded hover:bg-gray-50 dark:hover:bg-gray-700/50"
					onclick={() => toggle(trace.id)}
				>
					<div class="flex items-center gap-3">
						<span class="badge bg-gray-100 text-gray-700 dark:bg-gray-700 dark:text-gray-300 font-mono text-xs">
							{trace.action}
						</span>
						{#if trace.error}
							<span class="badge-failed">error</span>
						{/if}
					</div>
					<span class="text-xs text-gray-500 dark:text-gray-400">{formatTime(trace.timestamp)}</span>
				</button>
				{#if expandedId === trace.id}
					<div class="mx-3 mt-1 p-3 bg-gray-50 dark:bg-gray-900 rounded-lg">
						<pre class="text-xs text-gray-700 dark:text-gray-300 overflow-x-auto whitespace-pre-wrap">{JSON.stringify(trace.details, null, 2)}</pre>
						{#if trace.error}
							<p class="mt-2 text-xs text-red-600 dark:text-red-400">Error: {trace.error}</p>
						{/if}
					</div>
				{/if}
			</div>
		{/each}
	</div>
{/if}
