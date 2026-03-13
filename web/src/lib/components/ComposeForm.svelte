<script lang="ts">
	import { messages as messagesApi } from '$lib/api/client';

	let { onSent = () => {} }: { onSent?: () => void } = $props();

	let to = $state('');
	let body = $state('');
	let priority = $state(5);
	let subject = $state('');
	let sending = $state(false);
	let error = $state('');
	let showAdvanced = $state(false);

	async function handleSubmit(e: SubmitEvent) {
		e.preventDefault();
		error = '';

		if (!body.trim()) {
			error = 'Message body is required';
			return;
		}
		if (!to.trim()) {
			error = 'Recipient is required';
			return;
		}

		sending = true;
		try {
			await messagesApi.send({
				to: to.trim(),
				body: body.trim(),
				priority,
				subject: subject.trim() || undefined
			});
			to = '';
			body = '';
			priority = 5;
			subject = '';
			onSent();
		} catch (err: any) {
			error = err.message || 'Failed to send message';
		} finally {
			sending = false;
		}
	}
</script>

<form class="card p-4" onsubmit={handleSubmit}>
	<h3 class="font-medium text-gray-900 dark:text-gray-100 mb-3">New Message</h3>

	{#if error}
		<div class="mb-3 p-3 bg-red-50 dark:bg-red-900/30 border border-red-200 dark:border-red-800 rounded-lg text-sm text-red-700 dark:text-red-300">
			{error}
		</div>
	{/if}

	<div class="space-y-3">
		<input type="text" placeholder="Recipient agent name" class="input" bind:value={to} />
		<textarea
			placeholder="Message body"
			class="input min-h-[80px] resize-y"
			bind:value={body}
			rows="3"
		></textarea>

		{#if showAdvanced}
			<input type="text" placeholder="Subject (optional)" class="input" bind:value={subject} />
			<div class="flex items-center gap-2">
				<label class="text-sm text-gray-600 dark:text-gray-400">Priority:</label>
				<input type="range" min="1" max="10" class="flex-1" bind:value={priority} />
				<span class="text-sm font-medium w-6 text-center">{priority}</span>
			</div>
		{/if}

		<div class="flex items-center justify-between">
			<button type="button" class="text-sm text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200" onclick={() => (showAdvanced = !showAdvanced)}>
				{showAdvanced ? 'Hide' : 'Show'} options
			</button>
			<button type="submit" class="btn-primary" disabled={sending}>
				{sending ? 'Sending...' : 'Send'}
			</button>
		</div>
	</div>
</form>
