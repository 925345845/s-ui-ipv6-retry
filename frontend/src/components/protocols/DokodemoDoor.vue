<template>
  <v-card subtitle="Dokodemo-door">
    <v-row>
      <v-col cols="12" sm="6" md="4">
        <v-select v-model="data.network" :items="networks" :label="$t('network')" hide-details />
      </v-col>
      <v-col cols="12" sm="6" md="4">
        <v-text-field v-model="data.address" :label="$t('xray.targetAddress')" placeholder="127.0.0.1" hide-details />
      </v-col>
      <v-col cols="12" sm="6" md="4">
        <v-text-field v-model.number="data.port" :label="$t('xray.targetPort')" type="number" min="1" max="65535" hide-details />
      </v-col>
      <v-col cols="12" sm="6" md="4">
        <v-switch v-model="data.follow_redirect" color="primary" :label="$t('xray.followRedirect')" hide-details />
      </v-col>
      <v-col cols="12" sm="6" md="4">
        <v-switch v-model="sniffingEnabled" color="primary" :label="$t('xray.sniffing')" hide-details />
      </v-col>
      <v-col cols="12" sm="6" md="4" v-if="sniffingEnabled">
        <v-switch v-model="data.sniffing.routeOnly" color="primary" :label="$t('xray.routeOnly')" hide-details />
      </v-col>
    </v-row>
  </v-card>
</template>

<script lang="ts">
export default {
  props: ['data'],
  data() {
    return { networks: ['tcp,udp', 'tcp', 'udp'] }
  },
  computed: {
    sniffingEnabled: {
      get(): boolean { return this.$props.data.sniffing?.enabled === true },
      set(value: boolean) {
        this.$props.data.sniffing = value
          ? { enabled: true, destOverride: ['http', 'tls', 'quic'], routeOnly: true }
          : undefined
      },
    },
  },
}
</script>
