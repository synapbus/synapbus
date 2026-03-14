<script lang="ts">
	type Agent = {
		id: number;
		name: string;
		display_name: string;
		type: string;
		status: string;
		created_at: string;
	};

	let { agent, ownerName }: { agent: Agent; ownerName?: string | null } = $props();
</script>

<a href="/agents/{agent.name}" class="card p-4 block hover:border-border-active transition-all group">
	<div class="flex items-start justify-between">
		<div class="flex items-center gap-3">
			<div class="w-10 h-10 rounded-lg bg-bg-tertiary flex items-center justify-center text-sm font-bold text-text-secondary group-hover:text-text-primary transition-colors">
				{(agent.display_name || agent.name).charAt(0).toUpperCase()}
			</div>
			<div>
				<h3 class="font-semibold text-sm text-text-primary">{agent.display_name || agent.name}</h3>
				{#if agent.display_name}
					<p class="text-xs text-text-secondary font-mono">@{agent.name}</p>
				{/if}
				{#if ownerName}
					<p class="text-[10px] text-text-secondary mt-0.5">Owner: {ownerName}</p>
				{/if}
			</div>
		</div>
		<div class="flex items-center gap-2">
			<span class="badge {agent.type === 'ai' ? 'bg-accent-purple/20 text-accent-purple' : 'bg-accent-blue/20 text-accent-blue'}">
				{agent.type}
			</span>
			<span class="flex items-center gap-1 badge {agent.status === 'active' ? 'bg-accent-green/20 text-accent-green' : 'bg-bg-tertiary text-text-secondary'}">
				<span class="w-1.5 h-1.5 rounded-full {agent.status === 'active' ? 'bg-accent-green' : 'bg-text-secondary'}"></span>
				{agent.status}
			</span>
		</div>
	</div>
</a>
