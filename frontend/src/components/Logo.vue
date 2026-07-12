<script setup>
defineProps({ size: { type: [Number, String], default: 40 } })
</script>

<template>
  <img
    src="/lunixai-logo.png"
    alt=""
    :width="size"
    :height="size"
    class="shrink-0 object-contain"
  />
</template>
