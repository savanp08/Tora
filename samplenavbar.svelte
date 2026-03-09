<script lang="ts">
  import { page } from '$app/stores';
  import { onMount } from 'svelte';
  import { fade, fly, scale } from 'svelte/transition';

  export let isHighContrast = false;
  export let scrollY = 0; 
  
  const navLinks = [
    { label: 'HOME', href: '/' },
    { label: 'RESUME', href: '/resume' },
    { label: 'EXPERIENCE', href: '/experience' },
    { label: 'PROJECTS', href: '/projects' },
  ];

  let isHovered = false;
  let innerWidth = 0;
  let innerHeight = 0;

  // --- DESKTOP STATE ---
  $: isGhostMode = scrollY < 50 && !isHovered;
  $: activeLabel = navLinks.find(l => 
    l.href === '/' ? $page.url.pathname === '/' : $page.url.pathname.startsWith(l.href)
  )?.label || 'HOME';

  // --- MOBILE DRAGGABLE STATE ---
  let isMobileMenuOpen = false;
  let fabPosition = { x: 0, y: 0 }; 
  let isDragging = false;
  let isPressed = false; 
  let dragStartPos = { x: 0, y: 0 }; 
  let dragOffset = { x: 0, y: 0 };
  let fabElement: HTMLButtonElement;
  let isSnapping = false;

  // --- SMART MENU POSITIONING ---
  // Calculates where the menu should appear relative to the FAB
  $: menuPosition = (() => {
    const menuWidth = 200;
    const menuHeight = 220; // Approx height of menu
    const gap = 12; // Gap between button and menu

    let top = 0;
    let left = 0;

    // Vertical Logic: Prefer Below, switch to Above if near bottom
    if (fabPosition.y + 56 + gap + menuHeight > innerHeight) {
      top = fabPosition.y - menuHeight - gap; // Open Above
    } else {
      top = fabPosition.y + 56 + gap; // Open Below
    }

    // Horizontal Logic: Align Right edges if on right side, Left if on left
    if (fabPosition.x + 56/2 > innerWidth / 2) {
      left = (fabPosition.x + 56) - menuWidth; // Align Right
    } else {
      left = fabPosition.x; // Align Left
    }

    // Safety Clamp
    left = Math.max(10, Math.min(innerWidth - menuWidth - 10, left));

    return { top, left };
  })();

  function handleDragStart(e: MouseEvent | TouchEvent) {
    const target = e.target as HTMLElement;
    if (!target.closest('.holo-fab')) return;

    isPressed = true;
    isDragging = false;
    isSnapping = false; 

    const clientX = 'touches' in e ? e.touches[0].clientX : e.clientX;
    const clientY = 'touches' in e ? e.touches[0].clientY : e.clientY;
    
    dragStartPos = { x: clientX, y: clientY };
    const rect = fabElement.getBoundingClientRect();
    dragOffset.x = clientX - rect.left;
    dragOffset.y = clientY - rect.top;
  }

  function handleDragMove(e: MouseEvent | TouchEvent) {
    if (!isPressed) return;

    const clientX = 'touches' in e ? e.touches[0].clientX : e.clientX;
    const clientY = 'touches' in e ? e.touches[0].clientY : e.clientY;

    const moveX = Math.abs(clientX - dragStartPos.x);
    const moveY = Math.abs(clientY - dragStartPos.y);

    if (!isDragging && (moveX > 5 || moveY > 5)) {
      isDragging = true;
    }

    if (isDragging) {
      if ('touches' in e && e.cancelable) e.preventDefault();

      let newX = clientX - dragOffset.x;
      let newY = clientY - dragOffset.y;

      const padding = 15;
      const fabSize = 56;
      newX = Math.max(padding, Math.min(innerWidth - fabSize - padding, newX));
      newY = Math.max(padding, Math.min(innerHeight - fabSize - padding, newY));

      fabPosition = { x: newX, y: newY };
    }
  }

  function handleDragEnd() {
    isPressed = false;

    if (isDragging) {
      isSnapping = true; 
      
      const fabSize = 56;
      const padding = 20; 
      const centerX = fabPosition.x + fabSize / 2;
      
      if (centerX < innerWidth / 2) {
        fabPosition.x = padding; // Snap Left
      } else {
        fabPosition.x = innerWidth - fabSize - padding; // Snap Right
      }
      
      setTimeout(() => { isDragging = false; }, 50);
    } else {
      isDragging = false;
    }
  }

  function toggleMobileMenu() {
    if (!isDragging) {
      isMobileMenuOpen = !isMobileMenuOpen;
    }
  }

  onMount(() => {
    if (typeof window !== 'undefined') {
      // DEFAULT: Top Right
      fabPosition = { 
        x: window.innerWidth - 76, // 56px width + 20px padding
        y: 20 
      };
      isSnapping = true; 
    }
  });
</script>

<svelte:window 
  bind:innerWidth={innerWidth} 
  bind:innerHeight={innerHeight}
  on:mousemove={handleDragMove} 
  on:mouseup={handleDragEnd}
  on:touchmove={handleDragMove}
  on:touchend={handleDragEnd}
/>

{#if innerWidth > 768}
  <nav 
    class="desktop-nav" 
    class:high-contrast={isHighContrast}
    class:ghost-mode={isGhostMode}
    on:mouseenter={() => isHovered = true}
    on:mouseleave={() => isHovered = false}
  >
    <div class="glass-pill">
      {#each navLinks as link}
        <a 
          href={link.href}
          class="nav-item {activeLabel === link.label ? 'active' : ''}" 
          data-sveltekit-preload-data="hover" 
        >
          {link.label}
          {#if activeLabel === link.label}
            <div class="glow-dot" layoutId="glow"></div>
          {/if}
        </a>
      {/each}
    </div>
  </nav>

{:else}
  {#if isMobileMenuOpen}
    <div 
      class="mobile-overlay" 
      transition:fade={{ duration: 200 }}
      on:click={() => isMobileMenuOpen = false}
      role="button"
      tabindex="0"
      on:keydown={() => isMobileMenuOpen = false}
    ></div>

    <div 
      class="mobile-menu-card" 
      transition:scale={{ start: 0.9, duration: 200, origin: 'top right' }}
      style="top: {menuPosition.top}px; left: {menuPosition.left}px;"
      on:click|stopPropagation
      role="menu"
      tabindex="0"
      on:keydown={() => {}}
    >
      <div class="menu-header">SYSTEM_NAV</div>
      {#each navLinks as link}
        <a 
          href={link.href} 
          class="mobile-link {activeLabel === link.label ? 'active' : ''}"
          on:click={() => isMobileMenuOpen = false}
        >
          {link.label}
          {#if activeLabel === link.label}
            <span class="active-dot">●</span>
          {/if}
        </a>
      {/each}
    </div>
  {/if}

  <button 
    class="holo-fab"
    class:snapping={isSnapping}
    bind:this={fabElement}
    style="transform: translate({fabPosition.x}px, {fabPosition.y}px);"
    on:mousedown={handleDragStart}
    on:touchstart={handleDragStart}
    on:click={toggleMobileMenu}
    class:open={isMobileMenuOpen}
    aria-label="Toggle Menu"
  >
    <div class="fab-inner">
      {#if isMobileMenuOpen}
        <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="#00FFFF" stroke-width="2">
          <line x1="18" y1="6" x2="6" y2="18"></line>
          <line x1="6" y1="6" x2="18" y2="18"></line>
        </svg>
      {:else}
        <div class="grid-icon">
          <span></span><span></span>
          <span></span><span></span>
        </div>
      {/if}
    </div>
    <div class="fab-glow"></div>
  </button>

{/if}

<style>
  /* --- DESKTOP STYLES --- */
  .desktop-nav {
    position: fixed; top: 25px; left: 50%; transform: translateX(-50%); z-index: 1000;
    width: 24vw; min-width: 300px; max-width: 500px;
    transition: all 0.5s cubic-bezier(0.4, 0, 0.2, 1);
  }
  .glass-pill {
    display: flex; justify-content: space-between; align-items: center; width: 100%;
    padding: 0.6vw 1vw; background: rgba(255, 255, 255, 0.03);
    backdrop-filter: blur(20px) saturate(180%); border: 1px solid rgba(255, 255, 255, 0.1);
    border-radius: 999px; box-shadow: 0 4px 30px rgba(0, 0, 0, 0.5); transition: all 0.5s ease;
  }
  .ghost-mode .glass-pill { background: transparent; border-color: transparent; backdrop-filter: none; box-shadow: none; }
  .high-contrast:not(.ghost-mode) .glass-pill { background: rgba(0, 0, 0, 0.9); border-color: #5227FF; }
  .nav-item {
    text-decoration: none; background: none; border: none; position: relative;
    font-size: clamp(0.5rem, 0.75vw, 0.9rem); font-family: 'JetBrains Mono', monospace;
    font-weight: 500; color: rgba(255, 255, 255, 0.5); letter-spacing: 0.05em;
    padding: 0.4vw 0.8vw; border-radius: 99px; transition: all 0.3s ease;
  }
  .nav-item:hover { color: #fff; background: rgba(255, 255, 255, 0.1); }
  .nav-item.active { color: #fff; }
  .glow-dot {
    position: absolute; bottom: 2px; left: 50%; transform: translateX(-50%);
    width: 4px; height: 4px; background: #00FFFF; border-radius: 50%; box-shadow: 0 0 8px #00FFFF;
  }

  /* --- MOBILE WIDGET STYLES --- */
  
  .holo-fab {
    position: fixed; 
    top: 30vh; left: -20px; /* JS handles translate */
    width: 56px; height: 56px;
    z-index: 2000;
    background: rgba(10, 10, 12, 0.7);
    backdrop-filter: blur(12px);
    -webkit-backdrop-filter: blur(12px);
    border: 1px solid rgba(255, 255, 255, 0.1);
    border-radius: 18px; /* iOS Squircle */
    cursor: grab;
    touch-action: none;
    display: flex; align-items: center; justify-content: center;
    box-shadow: 0 10px 30px rgba(0,0,0,0.5);
    transition: box-shadow 0.3s, border-color 0.3s, background 0.3s;
    overflow: hidden; padding: 0;
    /* REMOVED: weird top/margin offsets from previous version */
  }

  .holo-fab.snapping {
    transition: transform 0.4s cubic-bezier(0.2, 0.9, 0.2, 1.2), box-shadow 0.3s;
  }

  .holo-fab:active { cursor: grabbing; transform: scale(0.95); }
  
  .holo-fab.open {
    border-color: #5227FF;
    background: rgba(82, 39, 255, 0.15);
    box-shadow: 0 0 20px rgba(82, 39, 255, 0.3);
  }

  .fab-inner { width: 100%; height: 100%; display: flex; align-items: center; justify-content: center; pointer-events: none; }

  .grid-icon { display: grid; grid-template-columns: 1fr 1fr; gap: 4px; width: 20px; height: 20px; }
  .grid-icon span { background: #fff; border-radius: 2px; width: 100%; height: 100%; opacity: 0.8; transition: 0.3s; }
  .holo-fab:hover .grid-icon span { background: #00FFFF; }

  .mobile-overlay { position: fixed; inset: 0; background: rgba(0, 0, 0, 0.6); backdrop-filter: blur(4px); z-index: 1998; }

  .mobile-menu-card {
    position: fixed; width: 200px;
    background: rgba(20, 20, 25, 0.95);
    border: 1px solid rgba(82, 39, 255, 0.3);
    border-radius: 20px; padding: 16px;
    display: flex; flex-direction: column; gap: 8px;
    box-shadow: 0 20px 60px rgba(0,0,0,0.9); z-index: 1999;
  }

  .menu-header { font-family: 'JetBrains Mono'; font-size: 0.6rem; color: #666; padding-bottom: 8px; border-bottom: 1px solid rgba(255,255,255,0.05); margin-bottom: 4px; letter-spacing: 1px; }
  .mobile-link { display: flex; justify-content: space-between; align-items: center; text-decoration: none; color: #aaa; font-family: 'Inter', sans-serif; font-weight: 600; font-size: 0.9rem; padding: 12px 16px; border-radius: 12px; transition: all 0.2s; }
  .mobile-link:hover { background: rgba(255,255,255,0.05); color: #fff; }
  .mobile-link.active { background: rgba(82, 39, 255, 0.15); color: #fff; }
  .active-dot { color: #00FFFF; font-size: 0.6rem; }
</style>