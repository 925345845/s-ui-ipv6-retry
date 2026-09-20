<template>
  <v-card :subtitle="$t('objects.transport')">
    <v-row>
      <v-col cols="12" sm="6" md="4">
        <v-select
          hide-details
          label="Xray Transport"
          :items="transportTypes"
          v-model="transport.type"
        ></v-select>
      </v-col>
      <v-col cols="12" sm="6" md="4" v-if="transport.type == 'xhttp'">
        <v-select
          hide-details
          label="XHTTP Mode"
          :items="xhttpModes"
          v-model="transport.mode"
        ></v-select>
      </v-col>
      <v-col cols="12" sm="6" md="4" v-if="hasHost">
        <v-text-field hide-details label="Host" v-model="transport.host"></v-text-field>
      </v-col>
      <v-col cols="12" sm="6" md="4" v-if="hasPath">
        <v-text-field hide-details :label="$t('transport.path')" v-model="transport.path"></v-text-field>
      </v-col>
      <v-col cols="12" sm="6" md="4" v-if="transport.type == 'grpc'">
        <v-text-field hide-details label="Service Name" v-model="transport.service_name"></v-text-field>
      </v-col>
      <v-col cols="12" sm="6" md="4" v-if="transport.type == 'grpc'">
        <v-text-field hide-details label="Authority" v-model="transport.authority"></v-text-field>
      </v-col>
      <v-col cols="12" sm="6" md="4" v-if="transport.type == 'grpc'">
        <v-switch hide-details color="primary" label="Multi mode" v-model="transport.multi_mode"></v-switch>
      </v-col>
      <template v-if="transport.type == 'kcp'">
        <v-col cols="12" sm="6" md="4">
          <v-text-field hide-details label="MTU" type="number" min="21" v-model.number="transport.mtu"></v-text-field>
        </v-col>
        <v-col cols="12" sm="6" md="4">
          <v-text-field hide-details label="TTI" type="number" min="10" max="1000" suffix="ms" v-model.number="transport.tti"></v-text-field>
        </v-col>
        <v-col cols="12" sm="6" md="4">
          <v-text-field hide-details :label="$t('stats.upload')" type="number" min="1" suffix="MB/s" v-model.number="transport.uplink_capacity"></v-text-field>
        </v-col>
        <v-col cols="12" sm="6" md="4">
          <v-text-field hide-details :label="$t('stats.download')" type="number" min="1" suffix="MB/s" v-model.number="transport.downlink_capacity"></v-text-field>
        </v-col>
      </template>
      <v-col cols="12" sm="6" md="4" v-if="supportsProxyProtocol">
        <v-switch hide-details color="primary" label="Proxy Protocol" v-model="transport.accept_proxy_protocol"></v-switch>
      </v-col>
      <v-col cols="12" sm="6" md="4" v-if="supportsTrustedXff">
        <v-combobox
          hide-details
          chips
          closable-chips
          multiple
          label="Trusted XFF Headers"
          v-model="transport.trusted_x_forwarded_for"
        ></v-combobox>
      </v-col>
    </v-row>
  </v-card>
</template>

<script lang="ts">
export default {
  props: ['data'],
  data() {
    return {
      transportTypes: [
        { title: 'XHTTP', value: 'xhttp' },
        { title: 'RAW', value: 'raw' },
        { title: 'mKCP', value: 'kcp' },
        { title: 'WebSocket', value: 'ws' },
        { title: 'gRPC', value: 'grpc' },
        { title: 'HTTPUpgrade', value: 'httpupgrade' },
      ],
      xhttpModes: ['auto', 'packet-up', 'stream-up', 'stream-one'],
    }
  },
  computed: {
    transport() {
      if (!this.$props.data.transport || Object.keys(this.$props.data.transport).length == 0) {
        this.$props.data.transport = { type: 'xhttp', path: '/xhttp', mode: 'auto' }
      }
      return this.$props.data.transport
    },
    hasHost(): boolean {
      return ['xhttp', 'ws', 'httpupgrade'].includes(this.transport.type)
    },
    hasPath(): boolean {
      return ['xhttp', 'ws', 'httpupgrade'].includes(this.transport.type)
    },
    supportsProxyProtocol(): boolean {
      return ['raw', 'tcp', 'ws', 'httpupgrade'].includes(this.transport.type)
    },
    supportsTrustedXff(): boolean {
      return ['xhttp', 'ws', 'grpc', 'httpupgrade'].includes(this.transport.type)
    },
  },
  watch: {
    'transport.type'(value: string) {
      if (value == 'xhttp') {
        this.transport.path = this.transport.path || '/xhttp'
        this.transport.mode = this.transport.mode || 'auto'
        this.transport.trusted_x_forwarded_for = this.transport.trusted_x_forwarded_for || []
      } else if (value == 'grpc') {
        this.transport.service_name = this.transport.service_name || ''
        this.transport.authority = this.transport.authority || ''
        this.transport.trusted_x_forwarded_for = this.transport.trusted_x_forwarded_for || []
      } else if (value == 'kcp') {
        this.transport.mtu = this.transport.mtu || 1350
        this.transport.tti = this.transport.tti || 50
        this.transport.uplink_capacity = this.transport.uplink_capacity || 5
        this.transport.downlink_capacity = this.transport.downlink_capacity || 20
        this.transport.cwnd_multiplier = this.transport.cwnd_multiplier || 2
        this.transport.max_sending_window = this.transport.max_sending_window || 2048
      } else if (['ws', 'httpupgrade'].includes(value)) {
        this.transport.path = this.transport.path || '/'
        this.transport.trusted_x_forwarded_for = this.transport.trusted_x_forwarded_for || []
      }
    },
  },
}
</script>
