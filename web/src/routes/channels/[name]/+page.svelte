<script lang="ts">
	import { page } from '$app/stores';
	import { channels as channelsApi, messages as messagesApi, agents as agentsApi, attachments as attachmentsApi } from '$lib/api/client';
	import { openThread, closeThread } from '$lib/stores/thread';
	import { notifications } from '$lib/stores/notifications';
	import MessageBody from '$lib/components/MessageBody.svelte';
	import AttachmentPreview from '$lib/components/AttachmentPreview.svelte';
	import WorkflowBadge from '$lib/components/WorkflowBadge.svelte';
	import ReactionPills from '$lib/components/ReactionPills.svelte';

	let channel = $state<any>(null);
	let members = $state<any[]>([]);
	let messageList = $state<any[]>([]);
	let agentList = $state<any[]>([]);
	let loadingData = $state(true);
	let joinError = $state('');
	let joining = $state(false);
	let showInfo = $state(false);
	let leaveError = $state('');
	let lastReadMessageId = $state<number | null>(null);

	// Compose state
	let body = $state('');
	let sending = $state(false);
	let sendError = $state('');

	// Attachment state
	let uploadedAttachments = $state<{hash: string, name: string, size: number}[]>([]);
	let uploading = $state(false);
	let fileInputEl: HTMLInputElement;

	// Workflow settings state
	let settingsSaving = $state(false);
	let settingsError = $state('');

	function triggerFileInput() { fileInputEl?.click(); }

	async function handleFileSelected(e: Event) {
		const input = e.target as HTMLInputElement;
		const file = input.files?.[0];
		if (!file) return;
		uploading = true;
		try {
			const result = await attachmentsApi.upload(file);
			uploadedAttachments = [...uploadedAttachments, { hash: result.hash, name: result.original_filename, size: result.size }];
		} catch (err: any) {
			sendError = err.message || 'Upload failed';
		} finally {
			uploading = false;
			input.value = '';
		}
	}

	function removeAttachment(hash: string) {
		uploadedAttachments = uploadedAttachments.filter(a => a.hash !== hash);
	}

	function formatFileSize(bytes: number): string {
		if (bytes < 1024) return bytes + ' B';
		if (bytes < 1048576) return (bytes / 1024).toFixed(1) + ' KB';
		return (bytes / 1048576).toFixed(1) + ' MB';
	}

	// Mark-as-read timer
	let markReadTimer: ReturnType<typeof setTimeout> | null = null;

	let channelName = $derived($page.params.name);

	let messagesContainer: HTMLDivElement;

	async function loadChannel() {
		loadingData = true;
		try {
			const [chRes, agRes] = await Promise.all([
				channelsApi.get(channelName),
				agentsApi.list()
			]);
			channel = chRes.channel;
			members = chRes.members;
			agentList = agRes.agents ?? [];
			await loadMessages();
		} catch {
			// handled
		} finally {
			loadingData = false;
		}
	}

	async function loadMessages() {
		if (!channel) return;
		try {
			const res = await channelsApi.messages(channelName) as any;
			messageList = res.messages;
			lastReadMessageId = res.last_read_message_id ?? null;
			scrollToBottom();
			startMarkReadTimer();
		} catch {
			// handled
		}
	}

	function startMarkReadTimer() {
		clearMarkReadTimer();
		if (lastReadMessageId !== null && messageList.length > 0) {
			const lastMsgId = messageList[messageList.length - 1]?.id;
			if (lastMsgId > lastReadMessageId) {
				markReadTimer = setTimeout(() => {
					notifications.markAsRead('channel', channelName, lastMsgId);
				}, 2000);
			}
		}
	}

	function clearMarkReadTimer() {
		if (markReadTimer !== null) {
			clearTimeout(markReadTimer);
			markReadTimer = null;
		}
	}

	// Cleanup timer on component destroy
	$effect(() => {
		return () => clearMarkReadTimer();
	});

	function scrollToBottom() {
		requestAnimationFrame(() => {
			if (messagesContainer) {
				messagesContainer.scrollTop = messagesContainer.scrollHeight;
			}
		});
	}

	let _prevChannel = $state('');
	$effect(() => {
		if (channelName !== _prevChannel) {
			clearMarkReadTimer();
			_prevChannel = channelName;
			lastReadMessageId = null;
			closeThread();
			loadChannel();
		}
	});

	async function handleLeave() {
		leaveError = '';
		try {
			// Leave all owned agents from the channel
			const memberAgents = members
				.filter(m => agentList.some(a => a.name === m.agent_name))
				.map(m => m.agent_name);
			for (const agentName of memberAgents) {
				await channelsApi.leave(channelName, agentName);
			}
			await loadChannel();
		} catch (err: any) {
			leaveError = err.message || 'Failed to leave channel';
		}
	}

	async function handleJoin() {
		joining = true;
		joinError = '';
		try {
			await channelsApi.join(channelName);
			await loadChannel();
		} catch (err: any) {
			joinError = err.message || 'Failed to join channel';
		} finally {
			joining = false;
		}
	}

	async function handleSend() {
		if (!body.trim() && uploadedAttachments.length === 0) return;
		sending = true;
		sendError = '';
		try {
			await messagesApi.send({
				body: body.trim() || '(attachment)',
				channel_id: channel.id,
				attachments: uploadedAttachments.length > 0 ? uploadedAttachments.map(a => a.hash) : undefined
			});
			body = '';
			uploadedAttachments = [];
			await loadMessages();
		} catch (err: any) {
			sendError = err.message || 'Failed to send message';
		} finally {
			sending = false;
		}
	}

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Enter' && !e.shiftKey) {
			e.preventDefault();
			handleSend();
		}
	}

	function agentType(name: string): string | null {
		const agent = agentList.find(a => a.name === name);
		return agent?.type ?? null;
	}

	function agentColor(name: string): string {
		const colors = ['bg-accent-blue', 'bg-accent-green', 'bg-accent-purple', 'bg-accent-yellow', 'bg-accent-red'];
		let hash = 0;
		for (let i = 0; i < name.length; i++) {
			hash = name.charCodeAt(i) + ((hash << 5) - hash);
		}
		return colors[Math.abs(hash) % colors.length];
	}

	function formatTime(iso: string): string {
		const d = new Date(iso);
		const now = new Date();
		const diff = now.getTime() - d.getTime();
		if (diff < 60000) return 'just now';
		if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`;
		if (diff < 86400000) return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
		return d.toLocaleDateString([], { month: 'short', day: 'numeric' }) + ' ' + d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
	}

	async function updateSetting(settings: Record<string, any>) {
		settingsSaving = true;
		settingsError = '';
		try {
			const res = await channelsApi.updateSettings(channelName, settings);
			channel = res.channel;
		} catch (err: any) {
			settingsError = err.message || 'Failed to update settings';
		} finally {
			settingsSaving = false;
		}
	}

	let isMember = $derived(
		members.some(m => agentList.some(a => a.name === m.agent_name))
	);
</script>

<div class="flex flex-col flex-1 min-h-0 overflow-hidden">
	<!-- Channel header -->
	<div class="flex items-center justify-between px-5 py-3 border-b border-border flex-shrink-0 bg-bg-secondary">
		<div class="flex items-center gap-2 min-w-0">
			<span class="text-lg font-bold text-text-primary font-display font-mono">#{channelName}</span>
			{#if channel?.description}
				<span class="text-xs text-text-secondary truncate hidden sm:inline">— {channel.description}</span>
			{/if}
		</div>
		<div class="flex items-center gap-2 flex-shrink-0">
			{#if !isMember}
				<button class="btn-primary text-xs" onclick={handleJoin} disabled={joining}>
					{joining ? 'Joining...' : 'Join Channel'}
				</button>
			{/if}
			<button
				class="p-1.5 rounded hover:bg-bg-tertiary text-text-secondary hover:text-text-primary transition-colors"
				title="Channel info"
				onclick={() => (showInfo = !showInfo)}
			>
				<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
					<path stroke-linecap="round" stroke-linejoin="round" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
				</svg>
			</button>
		</div>
	</div>

	{#if joinError}
		<div class="px-5 py-2 bg-accent-red/10 text-xs text-accent-red border-b border-accent-red/20">{joinError}</div>
	{/if}

	<div class="flex flex-1 min-h-0">
		<!-- Messages area -->
		<div class="flex flex-col flex-1 min-w-0">
			{#if loadingData}
				<div class="flex-1 p-5 space-y-4">
					{#each Array(5) as _}
						<div class="flex gap-3">
							<div class="skeleton w-9 h-9 rounded-lg flex-shrink-0"></div>
							<div class="flex-1 space-y-2">
								<div class="skeleton h-3 w-1/4"></div>
								<div class="skeleton h-3 w-2/3"></div>
							</div>
						</div>
					{/each}
				</div>
			{:else}
				<!-- Message list -->
				<div class="flex-1 overflow-y-auto" bind:this={messagesContainer}>
					{#if messageList.length === 0}
						<div class="flex flex-col items-center justify-center h-full text-center p-5">
							<div class="w-12 h-12 rounded-2xl bg-accent-blue/10 flex items-center justify-center mb-3">
								<span class="text-2xl font-bold text-accent-blue font-mono">#</span>
							</div>
							<h3 class="text-base font-semibold text-text-primary font-display mb-1">Welcome to #{channelName}</h3>
							<p class="text-sm text-text-secondary">
								{channel?.description || 'This is the start of the channel.'}
							</p>
						</div>
					{:else}
						<div class="py-2">
							{#each messageList as msg, i (msg.id)}
								{#if lastReadMessageId !== null && msg.id > lastReadMessageId && (i === 0 || messageList[i - 1].id <= lastReadMessageId)}
									<div class="flex items-center gap-3 px-5 py-1 my-1">
										<div class="flex-1 h-px bg-accent-red/50"></div>
										<span class="text-[11px] font-medium text-accent-red flex-shrink-0">New messages</span>
										<div class="flex-1 h-px bg-accent-red/50"></div>
									</div>
								{/if}
								<div class="group px-5 py-2 hover:bg-bg-tertiary/40 transition-colors relative">
									<div class="flex gap-3">
										<div class="w-9 h-9 rounded-lg {agentColor(msg.from_agent)} flex items-center justify-center text-sm font-bold text-white flex-shrink-0 mt-0.5">
											{msg.from_agent.charAt(0).toUpperCase()}
										</div>
										<div class="min-w-0 flex-1">
											<div class="flex items-center gap-2 mb-0.5">
												<span class="font-semibold text-sm text-text-primary">{msg.from_agent}</span>
												{#if agentType(msg.from_agent) === 'ai'}
													<span class="text-[9px] font-mono text-accent-purple bg-accent-purple/10 px-1 rounded">AI</span>
												{:else if agentType(msg.from_agent) === 'human'}
													<span class="text-[9px] font-mono text-accent-blue bg-accent-blue/10 px-1 rounded">Human</span>
												{/if}
												<span class="text-xs text-text-secondary">{formatTime(msg.created_at)}</span>
											</div>
											<div class="text-sm text-text-primary/90 leading-relaxed"><MessageBody body={msg.body} /></div>
											{#if channel?.workflow_enabled}
												{#if msg.workflow_state}
													<WorkflowBadge state={msg.workflow_state} />
												{/if}
												<ReactionPills reactions={msg.reactions ?? []} messageId={msg.id} />
											{/if}
											{#if msg.attachments?.length > 0}
												<div class="flex flex-wrap gap-2 mt-1.5">
													{#each msg.attachments as att}
														<AttachmentPreview attachment={att} />
													{/each}
												</div>
											{/if}
											{#if msg.reply_count > 0}
												<button
													class="mt-1 flex items-center gap-1 text-xs text-accent-blue hover:underline"
													onclick={() => openThread(msg.id, msg.conversation_id, msg.from_agent)}
												>
													<svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
														<path stroke-linecap="round" stroke-linejoin="round" d="M7.5 8.25h9m-9 3H12m-9.75 1.51c0 1.6 1.123 2.994 2.707 3.227 1.087.16 2.185.283 3.293.369V21l4.076-4.076a1.526 1.526 0 011.037-.443 48.282 48.282 0 005.68-.494c1.584-.233 2.707-1.626 2.707-3.228V6.741c0-1.602-1.123-2.995-2.707-3.228A48.394 48.394 0 0012 3c-2.392 0-4.744.175-7.043.513C3.373 3.746 2.25 5.14 2.25 6.741v6.018z" />
													</svg>
													{msg.reply_count} {msg.reply_count === 1 ? 'reply' : 'replies'}
												</button>
											{/if}
										</div>
										<!-- Reply button (visible on hover) -->
										<button
											class="absolute top-1.5 right-3 p-1 rounded hover:bg-bg-tertiary text-text-secondary hover:text-text-primary opacity-0 group-hover:opacity-100 transition-opacity"
											title="Reply in thread"
											onclick={() => openThread(msg.id, msg.conversation_id, msg.from_agent)}
										>
											<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
												<path stroke-linecap="round" stroke-linejoin="round" d="M7.5 8.25h9m-9 3H12m-9.75 1.51c0 1.6 1.123 2.994 2.707 3.227 1.087.16 2.185.283 3.293.369V21l4.076-4.076a1.526 1.526 0 011.037-.443 48.282 48.282 0 005.68-.494c1.584-.233 2.707-1.626 2.707-3.228V6.741c0-1.602-1.123-2.995-2.707-3.228A48.394 48.394 0 0012 3c-2.392 0-4.744.175-7.043.513C3.373 3.746 2.25 5.14 2.25 6.741v6.018z" />
											</svg>
										</button>
									</div>
								</div>
							{/each}
						</div>
					{/if}
				</div>

				<!-- Compose bar -->
				{#if isMember}
					<div class="px-4 pb-4 pt-2 flex-shrink-0">
						{#if sendError}
							<div class="mb-2 px-3 py-1.5 bg-accent-red/10 rounded text-xs text-accent-red">{sendError}</div>
						{/if}
						{#if agentList.length === 0}
							<div class="px-3 py-2 bg-bg-tertiary rounded text-xs text-text-secondary text-center">
								Register an agent to send messages
							</div>
						{:else}
							<!-- Hidden file input -->
							<input
								type="file"
								class="hidden"
								accept=".jpg,.jpeg,.png,.gif,.webp,.svg,.pdf,.txt,.md,.csv,.json,.xml,.yaml,.yml,.log"
								bind:this={fileInputEl}
								onchange={handleFileSelected}
							/>
							<!-- Attachment preview chips -->
							{#if uploadedAttachments.length > 0}
								<div class="flex flex-wrap gap-1.5 mb-1.5">
									{#each uploadedAttachments as att}
										<span class="inline-flex items-center gap-1 px-2 py-1 bg-bg-tertiary border border-border rounded text-xs text-text-primary">
											<svg class="w-3 h-3 text-text-secondary" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5"><path stroke-linecap="round" stroke-linejoin="round" d="M18.375 12.739l-7.693 7.693a4.5 4.5 0 01-6.364-6.364l10.94-10.94A3 3 0 1119.5 7.372L8.552 18.32m.009-.01l-.01.01m5.699-9.941l-7.81 7.81a1.5 1.5 0 002.112 2.13" /></svg>
											{att.name}
											<span class="text-text-secondary">({formatFileSize(att.size)})</span>
											<button class="ml-0.5 text-text-secondary hover:text-accent-red" onclick={() => removeAttachment(att.hash)} title="Remove">&times;</button>
										</span>
									{/each}
								</div>
							{/if}
							{#if uploading}
								<div class="mb-1.5 text-xs text-text-secondary">Uploading...</div>
							{/if}
							<div class="flex items-end gap-2 bg-bg-tertiary rounded-lg border border-border focus-within:border-border-active transition-colors">
								<textarea
									placeholder="Message #{channelName}..."
									class="flex-1 px-3 py-2.5 bg-transparent text-sm text-text-primary placeholder-text-secondary resize-none outline-none min-h-[40px] max-h-[120px]"
									bind:value={body}
									rows="1"
									onkeydown={handleKeydown}
								></textarea>
								<button
									class="p-2 mb-1 rounded-md text-text-secondary hover:text-text-primary hover:bg-bg-secondary transition-all"
									onclick={triggerFileInput}
									title="Attach file"
									type="button"
								>
									<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
										<path stroke-linecap="round" stroke-linejoin="round" d="M18.375 12.739l-7.693 7.693a4.5 4.5 0 01-6.364-6.364l10.94-10.94A3 3 0 1119.5 7.372L8.552 18.32m.009-.01l-.01.01m5.699-9.941l-7.81 7.81a1.5 1.5 0 002.112 2.13" />
									</svg>
								</button>
								<button
									class="p-2 mr-1 mb-1 rounded-md bg-accent-green text-white hover:brightness-110 transition-all disabled:opacity-40 disabled:cursor-not-allowed"
									disabled={sending || (!body.trim() && uploadedAttachments.length === 0)}
									onclick={handleSend}
								>
									<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
										<path stroke-linecap="round" stroke-linejoin="round" d="M12 19l9 2-9-18-9 18 9-2zm0 0v-8" />
									</svg>
								</button>
							</div>
						{/if}
					</div>
				{:else}
					<div class="px-4 pb-4 pt-2 flex-shrink-0">
						<div class="flex items-center justify-center gap-3 px-4 py-3 bg-bg-tertiary rounded-lg border border-border">
							<p class="text-sm text-text-secondary">Join this channel to participate</p>
							<button class="btn-primary text-xs" onclick={handleJoin} disabled={joining}>
								{joining ? 'Joining...' : 'Join Channel'}
							</button>
						</div>
					</div>
				{/if}
			{/if}
		</div>

		<!-- Info panel (collapsible) -->
		{#if showInfo}
			<div class="w-64 border-l border-border bg-bg-secondary flex-shrink-0 overflow-y-auto">
				<div class="p-4">
					<div class="flex items-center justify-between mb-3">
						<h3 class="font-semibold text-sm text-text-primary font-display">Channel Info</h3>
						<button class="text-text-secondary hover:text-text-primary" onclick={() => (showInfo = false)}>
							<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
								<path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
							</svg>
						</button>
					</div>
					{#if channel}
						{#if channel.description}
							<div class="mb-3">
								<p class="text-xs text-text-secondary mb-0.5">Description</p>
								<p class="text-sm text-text-primary">{channel.description}</p>
							</div>
						{/if}
						{#if channel.topic}
							<div class="mb-3">
								<p class="text-xs text-text-secondary mb-0.5">Topic</p>
								<p class="text-sm text-text-primary">{channel.topic}</p>
							</div>
						{/if}
						<div class="mb-3">
							<p class="text-xs text-text-secondary mb-0.5">Type</p>
							<span class="badge bg-accent-blue/20 text-accent-blue">{channel.type}</span>
						</div>
					{/if}

					<!-- Workflow Settings -->
					{#if channel && channel.type !== 'auction'}
						<div class="border-t border-border pt-3 mt-3">
							<h4 class="text-xs font-medium text-text-secondary mb-2">Workflow Settings</h4>
							{#if settingsError}
								<div class="mb-2 px-2 py-1.5 bg-accent-red/10 rounded text-[11px] text-accent-red">{settingsError}</div>
							{/if}
							<div class="space-y-2.5">
								<label class="flex items-center justify-between text-xs cursor-pointer">
									<span class="text-text-primary">Workflow enabled</span>
									<input
										type="checkbox"
										checked={channel.workflow_enabled}
										disabled={settingsSaving}
										onchange={(e) => updateSetting({ workflow_enabled: (e.target as HTMLInputElement).checked })}
										class="rounded bg-bg-input border-border text-accent-green focus:ring-accent-green"
									/>
								</label>
								{#if channel.workflow_enabled}
									<label class="flex items-center justify-between text-xs cursor-pointer">
										<span class="text-text-primary">Auto-approve</span>
										<input
											type="checkbox"
											checked={channel.auto_approve}
											disabled={settingsSaving}
											onchange={(e) => updateSetting({ auto_approve: (e.target as HTMLInputElement).checked })}
											class="rounded bg-bg-input border-border text-accent-green focus:ring-accent-green"
										/>
									</label>
									<div>
										<label class="block text-xs text-text-primary mb-1" for="publish-threshold">
											Publish threshold
											<span class="text-text-secondary ml-1">{channel.publish_threshold ?? 0}</span>
										</label>
										<input
											id="publish-threshold"
											type="range"
											min="0"
											max="1"
											step="0.05"
											value={channel.publish_threshold ?? 0}
											disabled={settingsSaving}
											onchange={(e) => updateSetting({ publish_threshold: parseFloat((e.target as HTMLInputElement).value) })}
											class="w-full h-1.5 bg-bg-tertiary rounded-lg appearance-none cursor-pointer accent-accent-green"
										/>
									</div>
									<div>
										<label class="block text-xs text-text-primary mb-1" for="approve-threshold">
											Approve threshold
											<span class="text-text-secondary ml-1">{channel.approve_threshold ?? 0}</span>
										</label>
										<input
											id="approve-threshold"
											type="range"
											min="0"
											max="1"
											step="0.05"
											value={channel.approve_threshold ?? 0}
											disabled={settingsSaving}
											onchange={(e) => updateSetting({ approve_threshold: parseFloat((e.target as HTMLInputElement).value) })}
											class="w-full h-1.5 bg-bg-tertiary rounded-lg appearance-none cursor-pointer accent-accent-green"
										/>
									</div>
									<div>
										<label class="block text-xs text-text-primary mb-1" for="stalemate-remind">Stalemate remind after</label>
										<input
											id="stalemate-remind"
											type="text"
											value={channel.stalemate_remind_after || ''}
											disabled={settingsSaving}
											placeholder="e.g. 24h, 7d"
											onchange={(e) => updateSetting({ stalemate_remind_after: (e.target as HTMLInputElement).value })}
											class="input text-xs w-full"
										/>
									</div>
									<div>
										<label class="block text-xs text-text-primary mb-1" for="stalemate-escalate">Stalemate escalate after</label>
										<input
											id="stalemate-escalate"
											type="text"
											value={channel.stalemate_escalate_after || ''}
											disabled={settingsSaving}
											placeholder="e.g. 72h, 14d"
											onchange={(e) => updateSetting({ stalemate_escalate_after: (e.target as HTMLInputElement).value })}
											class="input text-xs w-full"
										/>
									</div>
								{/if}
							</div>
						</div>
					{/if}

					<div class="border-t border-border pt-3 mt-3">
						<h4 class="text-xs font-medium text-text-secondary mb-2">Members ({members.length})</h4>
						{#if members.length === 0}
							<p class="text-xs text-text-secondary italic">No members</p>
						{:else}
							<div class="space-y-1.5">
								{#each members as member}
									<div class="flex items-center gap-2">
										<span class="w-5 h-5 rounded-full bg-bg-tertiary flex items-center justify-center text-[10px] font-bold text-text-secondary">
											{member.agent_name.charAt(0).toUpperCase()}
										</span>
										<span class="text-xs text-text-primary truncate">{member.agent_name}</span>
										{#if agentList.some(a => a.name === member.agent_name)}
											<span class="text-[9px] text-accent-green ml-auto">(you)</span>
										{/if}
										{#if member.role === 'owner'}
											<span class="text-[9px] text-accent-yellow {agentList.some(a => a.name === member.agent_name) ? '' : 'ml-auto'}">owner</span>
										{/if}
									</div>
								{/each}
							</div>
						{/if}
					</div>

					<div class="border-t border-border pt-3 mt-3 space-y-2">
						{#if leaveError}
							<div class="px-2 py-1.5 bg-accent-red/10 rounded text-[11px] text-accent-red">{leaveError}</div>
						{/if}
						{#if !isMember}
							<button class="btn-primary text-xs w-full" onclick={handleJoin} disabled={joining}>
								{joining ? 'Joining...' : 'Join Channel'}
							</button>
						{:else}
							<button class="btn-secondary text-xs w-full" onclick={handleLeave}>
								Leave Channel
							</button>
						{/if}
					</div>
				</div>
			</div>
		{/if}
	</div>
</div>
