<template>
  <v-card subtitle="WireGuard">
    <v-row>
      <v-col cols="12">
        <v-text-field
          v-model="data.secret_key"
          :label="$t('wireguard.secretKey')"
          hide-details
          dir="ltr"
        >
          <template #append-inner>
            <v-btn icon="mdi-refresh" size="small" variant="text" @click="genKey" :title="$t('actions.generate')" />
          </template>
        </v-text-field>
      </v-col>
      <v-col cols="12" sm="6" md="4">
        <v-text-field v-model.number="data.mtu" label="MTU" type="number" min="1280" max="1500" hide-details />
      </v-col>
      <v-col cols="12" sm="6" md="4">
        <v-text-field v-model.number="data.workers" :label="$t('wireguard.workers')" type="number" min="0" hide-details />
      </v-col>
    </v-row>

    <v-card-subtitle class="mt-2 px-0">
      {{ $t('wireguard.peers') }}
      <v-chip color="primary" density="compact" variant="elevated" class="ms-2" @click="addPeer">
        <v-icon icon="mdi-plus" />
      </v-chip>
    </v-card-subtitle>

    <v-card v-for="(peer, index) in data.peers" :key="index" variant="outlined" class="mb-3 pa-3">
      <div class="d-flex justify-space-between align-center mb-2">
        <span>Peer #{{ Number(index) + 1 }}</span>
        <v-btn icon="mdi-delete" size="small" color="error" variant="text" :disabled="data.peers.length <= 1" @click="data.peers.splice(index, 1)" />
      </div>
      <v-row>
        <v-col cols="12">
          <v-text-field v-model="peer.public_key" :label="$t('wireguard.publicKey')" hide-details dir="ltr" />
        </v-col>
        <v-col cols="12" sm="6">
          <v-combobox
            v-model="peer.allowed_ips"
            :label="$t('wireguard.allowedIPs')"
            multiple
            chips
            closable-chips
            hide-details
            dir="ltr"
          />
        </v-col>
        <v-col cols="12" sm="6">
          <v-text-field v-model="peer.endpoint" :label="$t('wireguard.endpoint')" placeholder="host:port" hide-details dir="ltr" />
        </v-col>
        <v-col cols="12" sm="6">
          <v-text-field v-model.number="peer.keep_alive" :label="$t('wireguard.keepAlive')" type="number" min="0" hide-details />
        </v-col>
        <v-col cols="12" sm="6">
          <v-text-field v-model="peer.pre_shared_key" :label="$t('wireguard.preSharedKey')" hide-details dir="ltr" />
        </v-col>
      </v-row>
    </v-card>
  </v-card>
</template>

<script lang="ts">
import HttpUtils from '@/plugins/httputil'

export default {
  props: ['data'],
  methods: {
    addPeer() {
      if (!this.$props.data.peers) this.$props.data.peers = []
      this.$props.data.peers.push({ public_key: '', allowed_ips: ['0.0.0.0/0', '::/0'] })
    },
    async genKey() {
      try {
        const msg = await HttpUtils.get('api/keypairs', { k: 'wireguard' })
        if (msg.success && msg.obj?.length) {
          // Server returns: PrivateKey: xxx / PublicKey: yyy
          for (const line of msg.obj) {
            const text = String(line || '').trim()
            if (text.startsWith('PrivateKey:')) {
              this.$props.data.secret_key = text.replace(/^PrivateKey:\s*/i, '').trim()
              break
            }
          }
        }
      } catch (e) {
        console.error(e)
      }
    },
  },
}
</script>
