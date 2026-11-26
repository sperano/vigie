<script lang="ts">
	import type { Row } from './lib/model';

	interface Props {
		/** The account usage readings, by name, as the server sends them. */
		usage: readonly Row[];
	}

	let { usage }: Props = $props();
</script>

{#if usage.length > 0}
	<ul class="usage" aria-label="account usage">
		{#each usage as account (account.name)}
			<li
				class="account {account.severity}"
				title={account.age === '-' ? undefined : `read ${account.age} ago`}
			>
				{#if account.mark}<span class="mark" aria-label="needs you">{account.mark}</span>{/if}
				{#if account.target}<a
						class="name"
						href={account.target}
						target="_blank"
						rel="noopener noreferrer">{account.label}</a
					>{:else}<span class="name">{account.label}</span>{/if}
				<span class="state">{account.state}</span>
				{#if account.detail}<span class="detail">{account.detail}</span>{/if}
			</li>
		{/each}
	</ul>
{/if}

<style>
	.usage {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		margin: 0;
		padding: 0;
		list-style: none;
		font-size: var(--font-size-xs);
	}

	.account {
		display: flex;
		gap: var(--space-1);
		align-items: baseline;
		padding: 0 var(--space-2);
		border-radius: var(--radius-pill);
		white-space: nowrap;
	}

	.name,
	.state {
		font-weight: 600;
	}

	.failed {
		color: var(--color-failed);
		background: var(--color-failed-bg);
	}
	.blocked {
		color: var(--color-blocked);
		background: var(--color-blocked-bg);
	}
	.busy {
		color: var(--color-busy);
		background: var(--color-busy-bg);
	}
	.attention {
		color: var(--color-attention);
		background: var(--color-attention-bg);
	}
	.quiet {
		color: var(--color-quiet);
		background: var(--color-quiet-bg);
	}
</style>
