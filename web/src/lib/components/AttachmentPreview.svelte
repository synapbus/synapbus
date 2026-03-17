<script lang="ts">
	type Attachment = {
		hash: string;
		original_filename: string;
		size: number;
		mime_type: string;
		is_image: boolean;
	};

	let { attachment }: { attachment: Attachment } = $props();

	let showOverlay = $state(false);

	function formatSize(bytes: number): string {
		if (bytes < 1024 * 1024) {
			return (bytes / 1024).toFixed(1) + ' KB';
		}
		return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
	}

	function openOverlay() {
		showOverlay = true;
	}

	function closeOverlay() {
		showOverlay = false;
	}

	function handleOverlayClick(e: MouseEvent) {
		if (e.target === e.currentTarget) {
			closeOverlay();
		}
	}

	function handleKeydown(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			closeOverlay();
		}
	}
</script>

<svelte:window onkeydown={showOverlay ? handleKeydown : undefined} />

{#if attachment.is_image}
	<!-- Image thumbnail -->
	<button
		class="block rounded-lg overflow-hidden border border-border hover:border-border-active transition-colors cursor-pointer bg-bg-tertiary"
		onclick={openOverlay}
	>
		<img
			src="/api/attachments/{attachment.hash}"
			alt={attachment.original_filename}
			class="max-w-[200px] max-h-[200px] object-cover"
			loading="lazy"
		/>
	</button>

	<!-- Fullscreen overlay -->
	{#if showOverlay}
		<div
			class="fixed inset-0 bg-black/80 z-50 flex items-center justify-center"
			role="dialog"
			aria-modal="true"
			onclick={handleOverlayClick}
		>
			<!-- Close button -->
			<button
				class="absolute top-4 right-4 p-2 rounded-lg bg-white/10 hover:bg-white/20 text-white transition-colors z-10"
				onclick={closeOverlay}
				aria-label="Close"
			>
				<svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
					<path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
				</svg>
			</button>

			<!-- Full-resolution image -->
			<img
				src="/api/attachments/{attachment.hash}"
				alt={attachment.original_filename}
				class="max-w-[90vw] max-h-[90vh] object-contain rounded-lg"
			/>

			<!-- Download button -->
			<div class="absolute bottom-6 left-1/2 -translate-x-1/2">
				<a
					href="/api/attachments/{attachment.hash}"
					download={attachment.original_filename}
					class="flex items-center gap-2 px-4 py-2 rounded-lg bg-white/10 hover:bg-white/20 text-white text-sm transition-colors"
				>
					<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
						<path stroke-linecap="round" stroke-linejoin="round" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4" />
					</svg>
					Download {attachment.original_filename}
				</a>
			</div>
		</div>
	{/if}
{:else}
	<!-- Non-image file -->
	<div class="inline-flex items-center gap-2 px-3 py-2 rounded-lg border border-border bg-bg-tertiary hover:bg-bg-tertiary/80 transition-colors">
		<!-- File icon -->
		<svg class="w-5 h-5 text-text-secondary flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
			<path stroke-linecap="round" stroke-linejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m2.25 0H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z" />
		</svg>
		<div class="min-w-0">
			<a
				href="/api/attachments/{attachment.hash}"
				download={attachment.original_filename}
				class="text-sm text-text-link hover:underline block truncate max-w-[200px]"
			>
				{attachment.original_filename}
			</a>
			<span class="text-[10px] text-text-secondary">{formatSize(attachment.size)}</span>
		</div>
	</div>
{/if}
