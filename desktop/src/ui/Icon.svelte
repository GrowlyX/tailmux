<script lang="ts">
  // A small inline icon set standing in for SF Symbols. 24-unit grid,
  // drawn with currentColor so icons take the label color.
  let { name, size = 14, stroke = 1.8 }: { name: string; size?: number; stroke?: number } = $props();
</script>

<svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width={stroke} stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
  {#if name === "gauge"}
    <path d="M3.5 16a8.5 8.5 0 1 1 17 0" /><path d="M12 16l3.5-5.5" /><circle cx="12" cy="16" r="1.4" fill="currentColor" /><path d="M6.5 14.5h1M16.5 14.5h1M12 7.5v1" stroke-width="1.5" />
  {:else if name === "grid"}
    <g fill="currentColor" stroke="none">
      <circle cx="5" cy="5" r="2.2" /><circle cx="12" cy="5" r="2.2" /><circle cx="19" cy="5" r="2.2" />
      <circle cx="5" cy="12" r="2.2" /><circle cx="12" cy="12" r="2.2" /><circle cx="19" cy="12" r="2.2" />
      <circle cx="5" cy="19" r="2.2" /><circle cx="12" cy="19" r="2.2" /><circle cx="19" cy="19" r="2.2" />
    </g>
  {:else if name === "computer"}
    <rect x="3" y="4" width="18" height="12" rx="2" /><path d="M8 20h8M12 16v4" />
  {:else if name === "gear"}
    <circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z" />
  {:else if name === "gear-fill"}
    <path fill="currentColor" stroke="none" d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z M12 15.2a3.2 3.2 0 1 1 0-6.4 3.2 3.2 0 0 1 0 6.4z" fill-rule="evenodd" />
  {:else if name === "lines"}
    <path d="M4 6h16M4 12h10M4 18h14" />
  {:else if name === "door"}
    <path d="M13 3H5v18h8" /><path d="M13 3l6 2v16l-6-2" fill="currentColor" fill-opacity="0.25" /><circle cx="15.5" cy="12.5" r="0.9" fill="currentColor" stroke="none" />
  {:else if name === "arrow-down"}
    <path d="M12 4v16M6 14l6 6 6-6" />
  {:else if name === "arrow-up"}
    <path d="M12 20V4M6 10l6-6 6 6" />
  {:else if name === "plus"}
    <path d="M12 5v14M5 12h14" />
  {:else if name === "ellipsis"}
    <circle cx="12" cy="12" r="9" /><circle cx="8" cy="12" r="1" fill="currentColor" /><circle cx="12" cy="12" r="1" fill="currentColor" /><circle cx="16" cy="12" r="1" fill="currentColor" />
  {:else if name === "warning"}
    <path d="M12 3 2 21h20L12 3z" /><path d="M12 10v5M12 18v.5" />
  {:else if name === "refresh"}
    <path d="M20 12a8 8 0 1 1-2.3-5.7" /><path d="M20 4v5h-5" />
  {:else if name === "download"}
    <circle cx="12" cy="12" r="9" /><path d="M12 7v9M8.5 12.5 12 16l3.5-3.5" />
  {:else if name === "copy"}
    <rect x="9" y="9" width="11" height="11" rx="2" /><path d="M5 15V6a2 2 0 0 1 2-2h9" />
  {:else if name === "search"}
    <circle cx="11" cy="11" r="6.5" /><path d="m20 20-4.3-4.3" />
  {:else if name === "check"}
    <path d="m5 12 5 5L20 7" />
  {/if}
</svg>
