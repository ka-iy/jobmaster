#!/bin/bash

# Create invalid certificates for testing.
#
# NOTE: TODO: IMPORTANT: Generating expired certificates requires that the
# 'faketime' program be installed on the system.

SUBJ_PREFIX="/C=US/ST=ALL/L=Everywhere/O=KartikeyaIYER"

# TODO: Accept these things as command line args to this script.
declare -a SERVER_LIST
SERVER_LIST+=( "server1" )

declare -a CLIENT_LIST
CLIENT_LIST+=( "client1" )

# Clean up intermediate CSRs
cleanup()
{
    find . -name "*_csr.pem" -exec rm -f {} \;
}

# Error-exit with given message
die()
{
    echo "ERROR: $1"
    cleanup
    exit 1
}

# Valid values for the CA
CERT_VALIDITY_DAYS=3650
ECPARAM_NAME="secp384r1"
SHA_TYPE="sha384"

generate_expired_ca()
{
    local cert_directory
    cert_directory="expired"
    [[ ! -d "${cert_directory}" ]] && mkdir ${cert_directory}
    pushd ${cert_directory} || die "Failed to cd to ${cert_directory}"
    cp ../openssl.cnf .

    faketime -f -100y openssl ecparam -name "${ECPARAM_NAME}" -genkey -noout -out expired_ca_key.pem || die "Failed generating expired_ca_key.pem"
    faketime -f -100y openssl req -x509 \
      -key expired_ca_key.pem \
      -nodes \
      -days ${CERT_VALIDITY_DAYS} \
      -out expired_ca_cert.pem \
      -subj ${SUBJ_PREFIX}/CN=jobmaster-expired_ca/ \
      -config ./openssl.cnf \
      -extensions jobmaster_ca \
      -${SHA_TYPE} || die "Failed generating expired CA cert"

    popd
}

generate_server_cert()
{
    local ecparam
    local sha
    local cert_directory

    ecparam=$1
    sha=$2
    cert_directory=$3

    [[ ! -d "${cert_directory}" ]] && mkdir ${cert_directory}
    pushd ${cert_directory} || die "Failed to cd to ${cert_directory}"
    cp ../openssl.cnf . || die "Failed to copy openssl.cnf to ${cert_directory}"
    cp ../../server_ca_key.pem . || die "Failed to copy ../../server_ca_key.pem to ${cert_directory}"
    cp ../../server_ca_cert.pem . || die "Failed to copy ./../server_ca_cert.pem to ${cert_directory}"

    cakey=server_ca_key.pem
    ca=server_ca_cert.pem
    days=365
    cmd_prefix=""
    if [[ "${cert_directory}" == "expired" ]];then
        cakey=expired_ca_key.pem
        ca=expired_ca_cert.pem
        days=1
        cmd_prefix="faketime -f -100y"
    fi

    # Generate the server cert(s).
    NUM_SERVERS=${#SERVER_LIST[@]}
    for i in ${!SERVER_LIST[@]}; do
        s="${SERVER_LIST[i]}"
        echo;echo "Generating SERVER certificate for ${s} (server $(( i+1 )) of ${NUM_SERVERS}) with ecparam=${ecparam}, sha=${sha}, directory=${cert_directory}"

        ${cmd_prefix} openssl ecparam -name "${ecparam}" -genkey -noout -out ${s}_key.pem || die "Failed generating ${s}_key.pem"

        ${cmd_prefix} openssl req -new \
          -key ${s}_key.pem \
          -out ${s}_csr.pem \
          -subj ${SUBJ_PREFIX}/CN=jobmaster-${s}/ \
          -config ./openssl.cnf \
          -reqexts jobmaster_server || die "Failed generating ${s}_csr.pem"

        ${cmd_prefix} openssl x509 -req \
          -in ${s}_csr.pem \
          -CAkey $cakey \
          -CA $ca \
          -days $days \
          -set_serial 1000 \
          -out ${s}_cert.pem \
          -extfile ./openssl.cnf \
          -extensions jobmaster_server \
          -${sha} || die "Failed generating ${s}_cert.pem"

        ${cmd_prefix} openssl x509 -startdate -enddate -noout -in ${s}_cert.pem
        ${cmd_prefix} openssl verify -verbose -CAfile ${ca}  ${s}_cert.pem || die "Failed verifying ${s}_cert.pem"
    done
    popd
}

generate_client_cert()
{
    local ecparam
    local sha
    local startdate
    local enddate
    local cert_directory

    ecparam=$1
    sha=$2
    cert_directory=$3

    [[ ! -d "${cert_directory}" ]] && mkdir ${cert_directory}
    pushd ${cert_directory} || die "Failed to cd to ${cert_directory}"
    cp ../openssl.cnf . || die "Failed to copy openssl.cnf to ${cert_directory}"
    cp ../../client_ca_key.pem . || die "Failed to copy ../../client_ca_key.pem to ${cert_directory}"
    cp ../../client_ca_cert.pem . || die "Failed to copy ./../client_ca_cert.pem to ${cert_directory}"

    cakey="client_ca_key.pem"
    ca="client_ca_cert.pem"
    days=365
    cmd_prefix=""
    if [[ "${cert_directory}" == "expired" ]];then
        cakey=expired_ca_key.pem
        ca=expired_ca_cert.pem
        days=1
        cmd_prefix="faketime -f -100y"
    fi


    # Generate the client certs.
    NUM_CLIENTS=${#CLIENT_LIST[@]}
    for i in ${!CLIENT_LIST[@]}; do
        c="${CLIENT_LIST[i]}"
        
        echo;echo "Generating CLIENT certificate for ${c} (client $(( i+1 )) of ${NUM_CLIENTS}) with ecparam=${ecparam}, sha=${sha}, directory=${cert_directory}"

        ${cmd_prefix} openssl ecparam -name "${ecparam}" -genkey -noout -out ${c}_key.pem || die "Failed generating ${c}_key.pem"

        ${cmd_prefix} openssl req -new \
          -key ${c}_key.pem \
          -out ${c}_csr.pem \
          -subj ${SUBJ_PREFIX}/CN=jobmaster-${c}/ \
          -config ./openssl.cnf \
          -reqexts jobmaster_client || die "Failed generating ${c}_csr.pem"

        ${cmd_prefix} openssl x509 -req \
          -in ${c}_csr.pem \
          -CAkey $cakey \
          -CA $ca \
          -days $days \
          -set_serial 1000 \
          -out ${c}_cert.pem \
          -extfile ./openssl.cnf \
          -extensions jobmaster_client \
          -${sha} || die "Failed generating ${c}_cert.pem"

        ${cmd_prefix} openssl x509 -startdate -enddate -noout -in ${c}_cert.pem
        ${cmd_prefix} openssl verify -verbose -CAfile ${ca}  ${c}_cert.pem || die "Failed verifying ${c}_cert.pem"
    done

    popd

}

# -------------------- main --------------------
which faketime || die "faketime not installed"

# Clean up from any previous bad runs
cleanup

# Generate expired certs
echo;echo "Generating expired certs..."
generate_expired_ca
generate_server_cert secp384r1 sha384 expired
generate_client_cert secp384r1 sha384 expired

# Generate certs with weak EC
echo;echo "Generating certs with weak EC crypto..."
generate_server_cert secp112r1 sha384 weakec
generate_client_cert secp112r1 sha384 weakec

# Generate certs with weak SHA
echo;echo "Generating certs with weak digest cipher..."
generate_server_cert secp384r1 sha1 weaksha
generate_client_cert secp384r1 sha1 weaksha

# Now generate a client certificate for a client not registered in the server.
CLIENT_LIST=( "client666" )
generate_client_cert secp384r1 sha384 unknownclient
cp ../server*.pem unknownclient

cleanup
exit 0
