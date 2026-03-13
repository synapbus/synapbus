<script lang="ts">
	type Agent = {
		id: number;
		name: string;
		display_name: string;
		type: string;
		status: string;
		created_at: string;
	};

	let { agent }: { agent: Agent } = $props();
</script>

<a href="/agents/{agent.name}" class="card p-4 block hover:shadow-md transition-shadow">
	<div class="flex items-start justify-between">
		<div>
			<h3 class="font-medium text-gray-900 dark:text-gray-100">{agent.display_name || agent.name}</h3>
			{#if agent.display_name}
				<p class="text-xs text-gray-500 dark:text-gray-400 mt-0.5">@{agent.name}</p>
			{/if}
		</div>
		<div class="flex items-center gap-2">
			<span class="badge {agent.type === 'ai' ? 'bg-purple-100 text-purple-700 dark:bg-purple-900 dark:text-purple-300' : 'bg-blue-100 text-blue-700 dark:bg-blue-900 dark:text-blue-300'}">
				{agent.type}
			</span>
			<span class="badge {agent.status === 'active' ? 'bg-green-100 text-green-700 dark:bg-green-900 dark:text-green-300' : 'bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-400'}">
				{agent.status}
			</span>
		</div>
	</div>
</a>
