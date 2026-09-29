<script lang="ts">
  // A macOS-style switch.
  let {
    on,
    tint = "var(--accent)",
    small = false,
    disabled = false,
    onchange,
  }: { on: boolean; tint?: string; small?: boolean; disabled?: boolean; onchange: (on: boolean) => void } = $props();
</script>

<button
  type="button"
  class="pill"
  class:on
  class:small
  role="switch"
  aria-checked={on}
  aria-label={on ? "on" : "off"}
  {disabled}
  style:--tint={tint}
  onclick={(e) => { e.stopPropagation(); onchange(!on); }}
>
  <span class="knob"></span>
</button>

<style>
  .pill {
    position: relative; width: 30px; height: 18px; flex: none;
    border-radius: 9px; border: none; padding: 0;
    background: rgb(var(--fg-rgb) / 0.15);
    transition: background 0.15s ease-out;
  }
  .pill.small { transform: scale(0.85); transform-origin: left center; }
  .pill.on { background: var(--tint); }
  .pill:disabled { opacity: 0.5; }
  .knob {
    position: absolute; top: 2px; left: 2px; width: 14px; height: 14px; border-radius: 50%;
    background: #fff; box-shadow: 0 0.5px 1.6px rgb(0 0 0 / 0.25);
    transition: left 0.15s ease-out;
  }
  .pill.on .knob { left: 14px; }
</style>
