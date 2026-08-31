import { onBeforeUnmount, onMounted, type Ref } from 'vue'

interface DismissibleAddMenuOptions {
  open: Readonly<Ref<boolean>>
  root: Readonly<Ref<HTMLElement | null>>
  onClose: () => void
}

/**
 * Keep the compact Add menu open while its trigger or any menu content owns
 * the interaction, and dismiss it when the user leaves the Add control. Capture
 * listeners make focus transitions reliable even when a child stops bubbling.
 */
export function useDismissibleAddMenu({ open, root, onClose }: DismissibleAddMenuOptions): void {
  function isInside(target: EventTarget | null): boolean {
    const container = root.value
    return Boolean(container && target instanceof Node && container.contains(target))
  }

  function handlePointerDown(event: PointerEvent) {
    if (!open.value || isInside(event.target)) return
    onClose()
  }

  function handleFocusIn(event: FocusEvent) {
    if (!open.value || isInside(event.target)) return
    onClose()
  }

  function handleKeydown(event: KeyboardEvent) {
    if (!open.value || event.key !== 'Escape') return
    event.preventDefault()
    event.stopPropagation()
    onClose()
  }

  onMounted(() => {
    document.addEventListener('pointerdown', handlePointerDown, true)
    document.addEventListener('focusin', handleFocusIn, true)
    document.addEventListener('keydown', handleKeydown, true)
  })

  onBeforeUnmount(() => {
    document.removeEventListener('pointerdown', handlePointerDown, true)
    document.removeEventListener('focusin', handleFocusIn, true)
    document.removeEventListener('keydown', handleKeydown, true)
  })
}
