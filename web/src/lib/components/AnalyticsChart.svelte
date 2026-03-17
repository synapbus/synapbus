<script lang="ts">
	let { buckets = [], span = '24h' }: { buckets: { time: string; count: number }[]; span?: string } = $props();

	const chartHeight = 160;
	const chartPadding = { top: 10, right: 10, bottom: 24, left: 40 };

	let containerWidth = $state(600);
	let containerEl: HTMLDivElement | undefined = $state(undefined);
	let hoveredIndex = $state<number | null>(null);

	$effect(() => {
		if (containerEl) {
			const observer = new ResizeObserver((entries) => {
				containerWidth = entries[0]?.contentRect.width ?? 600;
			});
			observer.observe(containerEl);
			return () => observer.disconnect();
		}
	});

	let maxCount = $derived(Math.max(...buckets.map(b => b.count), 1));
	let innerWidth = $derived(containerWidth - chartPadding.left - chartPadding.right);
	let innerHeight = $derived(chartHeight - chartPadding.top - chartPadding.bottom);
	let barWidth = $derived(buckets.length > 0 ? Math.max(2, (innerWidth / buckets.length) - 2) : 0);

	function formatLabel(time: string): string {
		if (span === '1h' || span === '4h') {
			// Show HH:MM
			const parts = time.split(' ');
			return parts[parts.length - 1] || time;
		}
		if (span === '24h') {
			// Show hour
			const match = time.match(/(\d{2}):00/);
			return match ? match[1] + 'h' : time;
		}
		// 7d, 30d — show date
		const match = time.match(/(\d{2})-(\d{2})$/);
		return match ? `${match[1]}/${match[2]}` : time;
	}

	// Show about 6-8 labels max
	let labelInterval = $derived(Math.max(1, Math.floor(buckets.length / 7)));
</script>

<div bind:this={containerEl} class="w-full relative">
	{#if buckets.length === 0}
		<div class="flex items-center justify-center h-[160px] text-sm text-text-secondary">
			No messages in this period
		</div>
	{:else}
		<svg width={containerWidth} height={chartHeight} class="overflow-visible">
			<!-- Y-axis gridlines -->
			{#each [0, 0.25, 0.5, 0.75, 1] as tick}
				<line
					x1={chartPadding.left}
					y1={chartPadding.top + innerHeight * (1 - tick)}
					x2={chartPadding.left + innerWidth}
					y2={chartPadding.top + innerHeight * (1 - tick)}
					stroke="var(--border)"
					stroke-width="0.5"
					stroke-dasharray={tick === 0 ? 'none' : '3,3'}
				/>
				<text
					x={chartPadding.left - 6}
					y={chartPadding.top + innerHeight * (1 - tick) + 4}
					text-anchor="end"
					fill="var(--text-secondary)"
					font-size="10"
				>
					{Math.round(maxCount * tick)}
				</text>
			{/each}

			<!-- Bars -->
			{#each buckets as bucket, i}
				{@const barHeight = (bucket.count / maxCount) * innerHeight}
				{@const x = chartPadding.left + (i * (innerWidth / buckets.length)) + 1}
				{@const y = chartPadding.top + innerHeight - barHeight}
				<rect
					{x}
					{y}
					width={barWidth}
					height={barHeight}
					rx="2"
					fill={hoveredIndex === i ? 'var(--accent-purple)' : 'var(--accent-blue)'}
					opacity={hoveredIndex === i ? 1 : 0.8}
					class="transition-all duration-100"
					onmouseenter={() => hoveredIndex = i}
					onmouseleave={() => hoveredIndex = null}
				/>
				<!-- X-axis labels -->
				{#if i % labelInterval === 0}
					<text
						x={x + barWidth / 2}
						y={chartHeight - 4}
						text-anchor="middle"
						fill="var(--text-secondary)"
						font-size="9"
					>
						{formatLabel(bucket.time)}
					</text>
				{/if}
			{/each}
		</svg>

		<!-- Tooltip -->
		{#if hoveredIndex !== null && buckets[hoveredIndex]}
			<div class="absolute top-0 left-1/2 -translate-x-1/2 bg-bg-secondary border border-border rounded px-2 py-1 text-xs shadow-lg pointer-events-none z-10">
				<span class="text-text-secondary">{buckets[hoveredIndex].time}</span>
				<span class="font-bold text-text-primary ml-1">{buckets[hoveredIndex].count} msgs</span>
			</div>
		{/if}
	{/if}
</div>
