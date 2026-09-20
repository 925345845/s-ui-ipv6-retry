<template>
  <v-card subtitle="Xray Hysteria2">
    <v-row>
      <v-col cols="12" sm="6" md="4">
        <v-text-field
          v-model.number="data.udp_idle_timeout"
          :label="$t('xray.udpIdleTimeout')"
          type="number"
          min="2"
          max="600"
          suffix="s"
          hide-details
        />
      </v-col>
    </v-row>
  </v-card>
</template>

<script lang="ts">
export default { props: ['data'] }
</script>
