<script lang="ts">
	let { items = [], title = '', linkPrefix = '', nameField = 'name', displayField = '' }:
		{ items: any[]; title: string; linkPrefix?: string; nameField?: string; displayField?: string } = $props();

	let maxCount = $derived(items.length > 0 ? Math.max(...items.map(i => i.count), 1) : 1);
</script>

<div class="card">
	<div class="px-4 py-2.5 border-b border-border">
		<h3 class="font-semibold text-xs text-text-primary font-display">{title}</h3>
	</div>
	{#if items.length === 0}
		<div class="p-4 text-xs text-text-secondary text-center">No data</div>
	{:else}
		<div class="divide-y divide-border">
			{#each items as item, i}
				<a
					href="{linkPrefix}{item[nameField]}"
					class="flex items-center gap-3 px-4 py-2.5 hover:bg-bg-tertiary/50 transition-colors"
				>
					<span class="text-xs font-bold text-text-secondary w-5 text-right">{i + 1}</span>
					<div class="flex-1 min-w-0">
						<p class="text-sm text-text-primary truncate">
							{displayField && item[displayField] ? item[displayField] : item[nameField]}
						</p>
						{#if displayField && item[displayField] && item[displayField] !== item[nameField]}
							<p class="text-[10px] text-text-secondary font-mono">@{item[nameField]}</p>
						{/if}
					</div>
					<div class="flex items-center gap-2 flex-shrink-0">
						<div class="w-20 h-1.5 rounded-full bg-bg-tertiary overflow-hidden">
							<div
								class="h-full rounded-full bg-accent-blue"
								style="width: {(item.count / maxCount) * 100}%"
							></div>
						</div>
						<span class="text-xs font-mono text-text-secondary w-8 text-right">{item.count}</span>
					</div>
				</a>
			{/each}
		</div>
	{/if}
</div>
