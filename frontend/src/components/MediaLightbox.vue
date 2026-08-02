<script setup>
// Full-screen preview: the enlarged image/video on a dark backdrop, plus a
// small top-right action row (copy-to-clipboard for images, download for
// both). Click the dark area (or Esc, handled by the parent) to close.
import { computed, ref, watch } from 'vue'
import Icon from './Icon.vue'

const props = defineProps({
  src: { type: String, required: true },     // resolved media URL
  kind: { type: String, default: 'image' },  // 'image' | 'video'
  prompt: { type: String, default: '' },
  meta: { type: String, default: '' },
  metaSub: { type: String, default: '' },
  downloadName: { type: String, default: '' },
})
const emit = defineEmits(['close'])

// Show the existing lightweight thumbnail immediately, then replace it after
// the single full-resolution <img> request has downloaded and decoded.
const imgRatio = ref(1)
const imageLoaded = ref(false)
const imageFailed = ref(false)
const thumbnailSrc = computed(() => {
  const [path, query] = props.src.split('?', 2)
  return `${path}.thumb.jpg${query ? `?${query}` : ''}`
})

watch(() => [props.src, props.kind], () => {
  imgRatio.value = 1
  imageLoaded.value = false
  imageFailed.value = false
}, { immediate: true })

function onThumbnailLoad(event) {
  const img = event.currentTarget
  if (img?.naturalHeight) imgRatio.value = img.naturalWidth / img.naturalHeight
}

async function onImageLoad(event) {
  const img = event.currentTarget
  const loadedSrc = img.getAttribute('src')
  try {
    await img.decode?.()
  } catch {
    // The load event already confirms usable image data; decoding can reject
    // when the element is detached while the lightbox is closing.
  }
  if (loadedSrc === props.src) imageLoaded.value = true
}

function onImageError() {
  imageFailed.value = true
  flash('原图加载失败')
}

const toast = ref('')
let toastTimer = null
function flash(msg) {
  toast.value = msg
  clearTimeout(toastTimer)
  toastTimer = setTimeout(() => (toast.value = ''), 1800)
}

async function copyImage() {
  try {
    const blob = await (await fetch(props.src)).blob()
    const pngBlob = blob.type === 'image/png'
      ? blob
      : await new Promise((resolve, reject) => {
          createImageBitmap(blob).then((bitmap) => {
            const canvas = document.createElement('canvas')
            canvas.width = bitmap.width
            canvas.height = bitmap.height
            const ctx = canvas.getContext('2d')
            if (!ctx) { reject(new Error('no canvas ctx')); return }
            ctx.drawImage(bitmap, 0, 0)
            canvas.toBlob((out) => {
              if (out) resolve(out)
              else reject(new Error('png convert failed'))
            }, 'image/png')
          }).catch(reject)
        })
    await navigator.clipboard.write([new ClipboardItem({ 'image/png': pngBlob })])
    flash('图片已复制')
  } catch {
    flash('复制失败')
  }
}
</script>

<template>
  <!-- Teleport to <body> so the overlay escapes the layout's `main` (relative
       z-10) stacking context — otherwise the fixed z-index sits BELOW the
       root-level sidebar (z-30) and the logo pokes through the backdrop. -->
  <Teleport to="body">
  <transition name="lb-fade" appear>
    <div class="fixed inset-0 z-[100] bg-black/90 flex items-center justify-center p-4"
         @click.self="emit('close')">
      <video v-if="kind === 'video'" :src="src" controls autoplay
             class="max-h-[94vh] max-w-[96vw] rounded-lg"
             controlslist="nodownload noremoteplayback noplaybackrate"
             disablepictureinpicture disableremoteplayback></video>
      <div v-else
           :style="{ width: `min(96vw, calc(94vh * ${imgRatio}))`, aspectRatio: imgRatio }"
           class="relative overflow-hidden rounded-lg bg-black">
        <img :src="thumbnailSrc" alt="" aria-hidden="true" draggable="false"
             @load="onThumbnailLoad"
             class="absolute inset-0 h-full w-full object-contain transition-opacity duration-200"
             :class="imageLoaded ? 'opacity-0' : 'opacity-100'" />
        <img :src="src" :alt="prompt || '生成图片'" draggable="false"
             decoding="async" fetchpriority="high"
             @load="onImageLoad" @error="onImageError"
             class="absolute inset-0 h-full w-full object-contain transition-opacity duration-200"
             :class="imageLoaded ? 'opacity-100' : 'opacity-0'" />
        <span v-if="!imageLoaded && !imageFailed"
              class="absolute left-1/2 top-1/2 h-7 w-7 -translate-x-1/2 -translate-y-1/2 animate-spin rounded-full border-2 border-white/30 border-t-white/90"></span>
      </div>

      <!-- actions: copy (images only) + download -->
      <div class="absolute top-4 right-4 flex gap-2">
        <button v-if="kind !== 'video'" @click.stop="copyImage" title="复制图片"
                class="w-9 h-9 rounded-lg bg-black/60 ring-1 ring-white/15 hover:bg-black/80 text-white grid place-items-center">
          <Icon name="copy" class="w-4 h-4" />
        </button>
        <a :href="src" :download="downloadName || src.split('/').pop()" @click.stop title="下载"
           class="w-9 h-9 rounded-lg bg-black/60 ring-1 ring-white/15 hover:bg-black/80 text-white grid place-items-center">
          <Icon name="download" class="w-4 h-4" />
        </a>
        <button @click.stop="emit('close')" title="关闭"
                class="w-9 h-9 rounded-lg bg-black/60 ring-1 ring-white/15 hover:bg-black/80 text-white grid place-items-center">
          <Icon name="close" class="w-4 h-4" />
        </button>
      </div>

      <div v-if="toast"
           class="absolute bottom-6 left-1/2 -translate-x-1/2 bg-slate-900 text-white text-xs px-4 py-2 rounded-lg shadow-lg">
        {{ toast }}
      </div>
    </div>
  </transition>
  </Teleport>
</template>

<style scoped>
.lb-fade-enter-active, .lb-fade-leave-active { transition: opacity 0.18s ease; }
.lb-fade-enter-from, .lb-fade-leave-to { opacity: 0; }
</style>
