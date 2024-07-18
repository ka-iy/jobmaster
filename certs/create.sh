#!/bin/bash

# create.sh generates SSL/TLS certificates for the clients and the servers.
# Client certificates are required for mTLS.
# The same root CA certificate is used for server certs and client certs.
#
# NOTE: Certificates use elliptic curve crypto, with:
#   Signature Algorithm : ecdsa-with-SHA256 ECDSA
#   Public Key Algorithm: NIST P-384 (384-bit EC) using secp384r1
#     secp384r1 : NIST/SECG curve over a 384 bit prime field
#     This is list as a "shall" in [1] along with secp256r1
#
# Example to view the details of a generated certificate:
#     openssl x509 -in client1_cert.pem -text -noout
#
# References:
#     1. https://nvlpubs.nist.gov/nistpubs/SpecialPublications/NIST.SP.800-52r2.pdf
#     2. https://nvlpubs.nist.gov/nistpubs/SpecialPublications/NIST.SP.800-56Ar3.pdf
#         (Appendix D)
#     3. https://nvlpubs.nist.gov/nistpubs/SpecialPublications/NIST.SP.800-56Ar3.pdf

SUBJ_PREFIX="/C=US/ST=ALL/L=Everywhere/O=KartikeyaIYER"

# TODO: Accept these things as command line args to this script.
declare -a SERVER_LIST
SERVER_LIST+=( "server1" )

declare -a CLIENT_LIST
CLIENT_LIST+=( "client1" "client2" "client3" )
CLIENT_LIST+=( "superuser" )

CERT_VALIDITY_DAYS=3650
ECPARAM_NAME="secp384r1"
SHA_TYPE="sha384"

# Clean up intermediate CSRs
cleanup()
{
    rm -f *_csr.pem
}

# Error-exit with given message
die()
{
    echo "ERROR: $1"
    cleanup
    exit 1
}

# Generates a CA certificate and key using $1 as the file prefix and extension suffix
generate_ca()
{
    [[ -z "$1" ]] && die "No prefix given, cannot generate CA files"

    echo "Generating ${1} CA cert and key..."
    openssl ecparam -name "${ECPARAM_NAME}" -genkey -noout -out ${1}_ca_key.pem || die "Failed generating ${1}_ca_key.pem"
    openssl req -x509 \
      -key ${1}_ca_key.pem \
      -nodes \
      -days ${CERT_VALIDITY_DAYS} \
      -out ${1}_ca_cert.pem \
      -subj ${SUBJ_PREFIX}/CN=jobmaster-${1}_ca/ \
      -config ./openssl.cnf \
      -extensions jobmaster_ca \
      -${SHA_TYPE} || die "Failed generating ${1} CA cert"
}

# -------------------- main --------------------

# Clean up from any previous bad runs
cleanup

# Create the Server Certificate Authority (CA) cert and key.
generate_ca server

# Create the Client CA cert and key.
generate_ca client

# Generate the server cert(s).
NUM_SERVERS=${#SERVER_LIST[@]}
for i in ${!SERVER_LIST[@]}; do
    s=${SERVER_LIST[i]}
    echo;echo "Generating SERVER certificate for ${s} (server $(( i+1 )) of ${NUM_SERVERS})"

    openssl ecparam -name "${ECPARAM_NAME}" -genkey -noout -out ${s}_key.pem || die "Failed generating ${s}_key.pem"

    openssl req -new \
      -key ${s}_key.pem \
      -out ${s}_csr.pem \
      -subj ${SUBJ_PREFIX}/CN=jobmaster-${s}/ \
      -config ./openssl.cnf \
      -reqexts jobmaster_server || die "Failed generating ${s}_csr.pem"

    openssl x509 -req \
      -in ${s}_csr.pem \
      -CAkey server_ca_key.pem \
      -CA server_ca_cert.pem \
      -days ${CERT_VALIDITY_DAYS} \
      -set_serial 1000 \
      -out ${s}_cert.pem \
      -extfile ./openssl.cnf \
      -extensions jobmaster_server \
      -${SHA_TYPE} || die "Failed generating ${s}_cert.pem"

    openssl x509 -enddate -noout -in ${s}_cert.pem
    openssl verify -verbose -CAfile server_ca_cert.pem  ${s}_cert.pem || die "Failed verifying ${s}_cert.pem"
done

# Generate the client certs.
NUM_CLIENTS=${#CLIENT_LIST[@]}
for i in ${!CLIENT_LIST[@]}; do
    c=${CLIENT_LIST[i]}
    echo;echo "Generating CLIENT certificate for ${c} (client $(( i+1 )) of ${NUM_CLIENTS})"

    openssl ecparam -name "${ECPARAM_NAME}" -genkey -noout -out ${c}_key.pem || die "Failed generating ${c}_key.pem"

    openssl req -new \
      -key ${c}_key.pem \
      -out ${c}_csr.pem \
      -subj ${SUBJ_PREFIX}/CN=jobmaster-${c}/ \
      -config ./openssl.cnf \
      -reqexts jobmaster_client || die "Failed generating ${c}_csr.pem"

    openssl x509 -req \
      -in ${c}_csr.pem \
      -CAkey client_ca_key.pem \
      -CA client_ca_cert.pem \
      -days ${CERT_VALIDITY_DAYS} \
      -set_serial 1000 \
      -out ${c}_cert.pem \
      -extfile ./openssl.cnf \
      -extensions jobmaster_client \
      -${SHA_TYPE} || die "Failed generating ${c}_cert.pem"

    openssl x509 -enddate -noout -in ${c}_cert.pem
    openssl verify -verbose -CAfile client_ca_cert.pem  ${c}_cert.pem || die "Failed verifying ${c}_cert.pem"
done

cleanup
exit 0
